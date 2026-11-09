package agent

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/adk/v2/tool/loadartifactstool"
	"google.golang.org/genai"

	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/types"
)

type searchArgs struct {
	Query    string   `json:"query" jsonschema:"Natural-language search terms or a question. Use the user's language."`
	ChatIDs  []int64  `json:"chat_ids,omitempty" jsonschema:"Optional chat IDs to search. Omit to search every allowed chat."`
	Types    []string `json:"types,omitempty" jsonschema:"Optional message types: text, photo, video, document, voice, audio, poll, story."`
	Tags     []string `json:"tags,omitempty" jsonschema:"Optional image/enrichment tags. Tags use any-match by default."`
	TagMatch string   `json:"tag_match,omitempty" jsonschema:"Tag matching mode: any (default) or all."`
	Mode     string   `json:"mode,omitempty" jsonschema:"Search mode: hybrid (default), keyword, or media."`
	Modality string   `json:"modality,omitempty" jsonschema:"Semantic target: all, text, image, audio, or video. Only relevant to hybrid/media searches."`
	Limit    int      `json:"limit,omitzero" jsonschema:"Maximum number of hits to return, capped by the server."`
	Offset   int      `json:"offset,omitzero" jsonschema:"Pagination offset, capped by the server."`
	Sort     string   `json:"sort,omitempty" jsonschema:"Optional ordering: relevance (default), newest, or oldest."`
}

type toolHit struct {
	MessageID      int64    `json:"message_id"`
	ChatID         int64    `json:"chat_id"`
	ChatTitle      string   `json:"chat_title,omitempty"`
	UserID         int64    `json:"user_id,omitempty"`
	UserName       string   `json:"user_name,omitempty"`
	Type           string   `json:"type"`
	Timestamp      string   `json:"timestamp"`
	Message        string   `json:"message,omitempty"`
	OCRed          string   `json:"ocred,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Extracted      string   `json:"extracted,omitempty"`
	AIGenerated    string   `json:"aigenerated,omitempty"`
	Link           string   `json:"link,omitempty"`
	Media          bool     `json:"media"`
	MediaArtifacts []string `json:"media_artifacts,omitempty"`
}

type searchOutput struct {
	Count int       `json:"count"`
	Total int64     `json:"total"`
	Hits  []toolHit `json:"hits,omitempty"`
	Note  string    `json:"note,omitempty"`
}

type getMessagesArgs struct {
	ChatID       int64   `json:"chat_id" jsonschema:"Chat ID that owns the messages. It must be in the allowed scope."`
	MessageIDs   []int64 `json:"message_ids" jsonschema:"Message IDs to read, at most 5."`
	IncludeMedia bool    `json:"include_media,omitempty" jsonschema:"Load image/audio/video artifacts only when the user explicitly asks about media content."`
}

type getMessagesOutput struct {
	Messages []toolHit `json:"messages,omitempty"`
	Note     string    `json:"note,omitempty"`
}

type listChatsArgs struct{}

type chatInfo struct {
	ChatID int64  `json:"chat_id"`
	Title  string `json:"title,omitempty"`
	Type   int    `json:"type"`
	Public bool   `json:"public"`
}

type listChatsOutput struct {
	Chats []chatInfo `json:"chats"`
	Note  string     `json:"note,omitempty"`
}

type toolset struct {
	searcher    engine.Searcher
	mediaLoader MediaLoader
	allowed     []int64
	allowedSet  map[int64]struct{}
	maxResults  int
	searchCalls atomic.Int32

	lastMu    sync.Mutex
	lastQuery string
	lastHits  []toolHit
}

func (s *Service) tools(scope Scope) ([]tool.Tool, *toolset, error) {
	allowed := append([]int64(nil), scope.AllowedChatIDs...)
	sort.Slice(allowed, func(i, j int) bool { return allowed[i] < allowed[j] })
	allowed = slices.Compact(allowed)
	if len(allowed) == 0 {
		return nil, nil, fmt.Errorf("no allowed chats")
	}
	allowedSet := make(map[int64]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	maxResults := s.cfg.Agent.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	t := &toolset{searcher: s.searcher, mediaLoader: s.mediaLoader, allowed: allowed, allowedSet: allowedSet, maxResults: maxResults}

	searchTool, err := functiontool.New(functiontool.Config{
		Name:        "search_messages",
		Description: "Search indexed Telegram messages. Returns bounded metadata and links for text and multimedia messages.",
	}, t.searchMessages)
	if err != nil {
		return nil, nil, err
	}
	messagesTool, err := functiontool.New(functiontool.Config{
		Name:        "get_messages",
		Description: "Read full indexed details for specific message IDs in one allowed chat. Set include_media=true only when the user asks about actual image/audio/video content.",
	}, t.getMessages)
	if err != nil {
		return nil, nil, err
	}
	chatsTool, err := functiontool.New(functiontool.Config{
		Name:        "list_chats",
		Description: "List the chats this agent is allowed to search. Use this when the user's target chat is ambiguous.",
	}, t.listChats)
	if err != nil {
		return nil, nil, err
	}
	return []tool.Tool{chatsTool, searchTool, messagesTool, loadartifactstool.New()}, t, nil
}

func (t *toolset) fallbackAnswer() string {
	t.lastMu.Lock()
	hits := append([]toolHit(nil), t.lastHits...)
	query := t.lastQuery
	t.lastMu.Unlock()
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("搜索预算已用尽，以下是已找到的结果")
	if query != "" {
		b.WriteString("（查询：" + query + "）")
	}
	b.WriteString("：\n")
	for i, hit := range hits {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&b, "- [%s] %s\n  %s\n", hit.Type, truncateRunes(hit.Message, 240), hit.Link)
	}
	b.WriteString("如需更精确的结果，请缩小关键词或指定聊天范围。")
	return b.String()
}

func (t *toolset) resolveChats(requested []int64) ([]int64, error) {
	if len(requested) == 0 {
		return t.allowed, nil
	}
	out := make([]int64, 0, len(requested))
	for _, id := range requested {
		if _, ok := t.allowedSet[id]; !ok {
			return nil, fmt.Errorf("chat_id %d is outside the allowed search scope", id)
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return t.allowed, nil
	}
	return out, nil
}

func (t *toolset) searchMessages(ctx adkagent.Context, args searchArgs) (searchOutput, error) {
	if t.searchCalls.Add(1) > 3 {
		return searchOutput{}, fmt.Errorf("search budget exhausted; answer with the results already returned")
	}
	query := strings.TrimSpace(args.Query)
	if len(query) > 1000 {
		return searchOutput{}, fmt.Errorf("query is too long")
	}
	mode := strings.ToLower(strings.TrimSpace(args.Mode))
	switch mode {
	case "", "hybrid", "keyword", "media":
	default:
		return searchOutput{}, fmt.Errorf("invalid mode %q", args.Mode)
	}
	modality := strings.ToLower(strings.TrimSpace(args.Modality))
	switch modality {
	case "", "all", "text", "image", "audio", "video":
	default:
		return searchOutput{}, fmt.Errorf("invalid modality %q", args.Modality)
	}
	if query == "" && mode != "keyword" {
		return searchOutput{}, fmt.Errorf("query is required")
	}
	chats, err := t.resolveChats(args.ChatIDs)
	if err != nil {
		return searchOutput{}, err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > t.maxResults {
		limit = t.maxResults
	}
	if limit > 50 {
		limit = 50
	}
	offset := args.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > 100 {
		return searchOutput{}, fmt.Errorf("offset is too large")
	}
	typeFilters := make([]types.MessageType, 0, len(args.Types))
	for _, raw := range args.Types {
		mt, ok := types.MessageTypeFromString[strings.ToLower(strings.TrimSpace(raw))]
		if !ok {
			return searchOutput{}, fmt.Errorf("unknown message type %q", raw)
		}
		typeFilters = append(typeFilters, mt)
	}
	tagMatch := strings.ToLower(strings.TrimSpace(args.TagMatch))
	switch tagMatch {
	case "", "any", "all":
	default:
		return searchOutput{}, fmt.Errorf("invalid tag_match %q", args.TagMatch)
	}
	var sorts []string
	switch strings.ToLower(strings.TrimSpace(args.Sort)) {
	case "", "relevance", "relevant":
	case "newest", "new", "latest":
		sorts = []string{"timestamp:desc"}
	case "oldest", "old":
		sorts = []string{"timestamp:asc"}
	default:
		return searchOutput{}, fmt.Errorf("invalid sort %q", args.Sort)
	}

	req := types.SearchRequest{
		ChatIDs:     chats,
		Query:       query,
		TypeFilters: typeFilters,
		Tags:        args.Tags,
		TagMatch:    tagMatch,
		Mode:        mode,
		Modality:    modality,
		Limit:       int64(limit),
		Offset:      int64(offset),
		Sort:        sorts,
	}
	resp, err := t.searcher.Search(ctx, req)
	if err != nil {
		return searchOutput{}, fmt.Errorf("search failed: %w", err)
	}
	if resp == nil {
		return searchOutput{}, fmt.Errorf("search returned no response")
	}
	out := searchOutput{
		Count: len(resp.Hits),
		Total: resp.EstimatedTotalHits,
		Hits:  make([]toolHit, 0, len(resp.Hits)),
	}
	payloadChars := 0
	for _, hit := range resp.Hits {
		h := t.makeHit(ctx, hit)
		hChars := len(h.Message) + len(h.OCRed) + len(h.Extracted) + len(h.AIGenerated) + len(h.Link) + 32*len(h.Tags)
		if len(out.Hits) > 0 && payloadChars+hChars > 8000 {
			out.Note = "Additional hits omitted to keep the agent context small."
			break
		}
		out.Hits = append(out.Hits, h)
		payloadChars += hChars
	}
	t.lastMu.Lock()
	t.lastQuery = query
	t.lastHits = append([]toolHit(nil), out.Hits...)
	t.lastMu.Unlock()
	if len(out.Hits) == 0 {
		out.Note = "No matching messages found. Try broader or different search terms."
	}
	return out, nil
}

func (t *toolset) getMessages(ctx adkagent.Context, args getMessagesArgs) (getMessagesOutput, error) {
	if _, ok := t.allowedSet[args.ChatID]; !ok {
		return getMessagesOutput{}, fmt.Errorf("chat_id %d is outside the allowed search scope", args.ChatID)
	}
	if len(args.MessageIDs) == 0 || len(args.MessageIDs) > 5 {
		return getMessagesOutput{}, fmt.Errorf("message_ids must contain 1 to 5 IDs")
	}
	ids := make([]int, 0, len(args.MessageIDs))
	for _, id := range args.MessageIDs {
		if id <= 0 {
			return getMessagesOutput{}, fmt.Errorf("invalid message id %d", id)
		}
		ids = append(ids, int(id))
	}
	docs, err := t.searcher.GetDocuments(ctx, args.ChatID, ids)
	if err != nil {
		return getMessagesOutput{}, fmt.Errorf("read messages: %w", err)
	}
	out := getMessagesOutput{Messages: make([]toolHit, 0, len(docs))}
	mediaCount := 0
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		hit := t.makeHit(ctx, types.SearchHit{MessageDocument: *doc})
		if args.IncludeMedia && mediaCount < 2 {
			t.attachMedia(ctx, doc, &hit)
			mediaCount += len(hit.MediaArtifacts)
		}
		out.Messages = append(out.Messages, hit)
	}
	if len(out.Messages) == 0 {
		out.Note = "No messages found for those IDs."
	}
	return out, nil
}

func (t *toolset) listChats(ctx adkagent.Context, _ listChatsArgs) (listChatsOutput, error) {
	out := listChatsOutput{Chats: make([]chatInfo, 0, len(t.allowed))}
	for _, id := range t.allowed {
		info := chatInfo{ChatID: id}
		if chat, err := database.GetIndexChat(ctx, id); err == nil && chat != nil {
			info.Title = chat.Title
			info.Type = chat.Type
			info.Public = chat.Public
		}
		out.Chats = append(out.Chats, info)
	}
	return out, nil
}

func (t *toolset) attachMedia(ctx adkagent.Context, doc *types.MessageDocument, hit *toolHit) {
	if t.mediaLoader == nil || ctx.Artifacts() == nil || len(hit.MediaArtifacts) >= 2 {
		return
	}
	switch types.MessageType(doc.Type) {
	case types.MessageTypePhoto, types.MessageTypeVideo, types.MessageTypeVoice, types.MessageTypeAudio:
	default:
		return
	}
	data, mimeType, name, err := t.mediaLoader.LoadMedia(ctx, doc.ChatID, int(doc.ID))
	if err != nil || len(data) == 0 {
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	artifactName := fmt.Sprintf("btts_%d_%d%s", doc.ChatID, doc.ID, ext)
	if _, err := ctx.Artifacts().Save(ctx, artifactName, genai.NewPartFromBytes(data, mimeType)); err != nil {
		return
	}
	hit.MediaArtifacts = append(hit.MediaArtifacts, artifactName)
}

func (t *toolset) makeHit(ctx adkagent.Context, hit types.SearchHit) toolHit {
	doc := hit.MessageDocument
	chatTitle := ""
	if chat, err := database.GetIndexChat(ctx, doc.ChatID); err == nil && chat != nil {
		chatTitle = chat.Title
	}
	userName := ""
	if user, err := database.GetUserInfo(ctx, doc.UserID); err == nil && user != nil {
		userName = strings.TrimSpace(user.FirstName + " " + user.LastName)
	}
	msgType := types.MessageType(doc.Type)
	return toolHit{
		MessageID:   doc.ID,
		ChatID:      doc.ChatID,
		ChatTitle:   truncateRunes(chatTitle, 160),
		UserID:      doc.UserID,
		UserName:    truncateRunes(userName, 160),
		Type:        types.MessageTypeToString[msgType],
		Timestamp:   time.Unix(doc.Timestamp, 0).Format(time.RFC3339),
		Message:     truncateRunes(doc.Message, 400),
		OCRed:       truncateRunes(doc.Ocred, 200),
		Tags:        truncateStrings(doc.Tags, 16),
		Extracted:   truncateRunes(doc.Extracted, 300),
		AIGenerated: truncateRunes(doc.AIGenerated, 200),
		Link:        hit.MessageLink(),
		Media:       msgType != types.MessageTypeText,
	}
}

func truncateStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:limit]...)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
