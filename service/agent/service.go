package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/rs/xid"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/genai"

	"github.com/krau/btts/config"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/utils/cache"
)

const appName = "btts_search_agent"

// Scope is the hard boundary for an agent invocation. Tools only see these
// chat IDs and cannot widen their own search scope.
type Scope struct {
	UserID         string
	AllowedChatIDs []int64
	CurrentChatID  int64
	ConversationID string
}

// Attachment is an inline file supplied by the user with the question.
type Attachment struct {
	Data []byte
	MIME string
}

// Service runs a ReAct-style search agent with tools backed by the local
// btts searcher.
type MediaLoader interface {
	LoadMedia(ctx context.Context, chatID int64, messageID int) (data []byte, mime, name string, err error)
}

type Service struct {
	model       model.LLM
	searcher    engine.Searcher
	cfg         config.AppConfig
	mediaLoader MediaLoader
	sem         chan struct{}
}

var (
	defaultMu  sync.RWMutex
	defaultSvc *Service
)

func (s *Service) SetMediaLoader(loader MediaLoader) {
	if s != nil {
		s.mediaLoader = loader
	}
}

func SetDefault(s *Service) {
	defaultMu.Lock()
	defaultSvc = s
	defaultMu.Unlock()
}

func Default() *Service {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultSvc
}

// New creates the agent service. It returns nil when agent.enable is false.
func New(ctx context.Context, searcher engine.Searcher) (*Service, error) {
	cfg := config.C
	if !cfg.Agent.Enable {
		return nil, nil
	}
	if searcher == nil {
		return nil, errors.New("agent requires a search engine")
	}
	llm, err := newModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create agent model: %w", err)
	}
	maxConcurrency := cfg.Agent.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	log.FromContext(ctx).Infof("Agent enabled (provider=%s model=%s max_llm_calls=%d max_results=%d)",
		cfg.Agent.Provider, cfg.Agent.Model, cfg.Agent.MaxLLMCalls, cfg.Agent.MaxResults)
	return &Service{
		model:    llm,
		searcher: searcher,
		cfg:      cfg,
		sem:      make(chan struct{}, maxConcurrency),
	}, nil
}

func newModel(ctx context.Context, cfg config.AppConfig) (model.LLM, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Agent.Provider))
	switch provider {
	case "gemini", "google":
		return gemini.NewModel(ctx, cfg.Agent.Model, &genai.ClientConfig{
			APIKey: cfg.Agent.APIKey,
		})
	case "openai", "openai-compatible", "ollama":
		baseURL := strings.TrimSpace(cfg.Agent.BaseURL)
		apiKey := cfg.Agent.APIKey
		if provider == "ollama" {
			if baseURL == "" {
				baseURL = "http://127.0.0.1:11434/v1"
			}
			if apiKey == "" {
				apiKey = "ollama"
			}
		}
		api := openaimodel.APIResponses
		if provider == "ollama" || strings.EqualFold(cfg.Agent.API, "chat_completions") || strings.EqualFold(cfg.Agent.API, "chat") {
			api = openaimodel.APIChatCompletions
		}
		return openaimodel.NewModel(ctx, cfg.Agent.Model, &openaimodel.ClientConfig{
			APIKey:  apiKey,
			BaseURL: baseURL,
			API:     api,
		})
	default:
		return nil, fmt.Errorf("unsupported agent provider %q (use gemini, openai, or ollama)", provider)
	}
}

// Answer runs one bounded ReAct loop and returns the final assistant text.
func (s *Service) Answer(ctx context.Context, query string, scope Scope) (string, error) {
	return s.AnswerWithAttachments(ctx, query, scope, nil)
}

// AnswerWithAttachments runs the agent with inline user attachments such as images.
func (s *Service) AnswerWithAttachments(ctx context.Context, query string, scope Scope, attachments []Attachment) (string, error) {
	if s == nil {
		return "", errors.New("agent is not enabled")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("query is empty")
	}
	if len(scope.AllowedChatIDs) == 0 {
		return "", errors.New("no chats are available to the agent")
	}
	if scope.UserID == "" {
		scope.UserID = "anonymous"
	}
	conversationID := strings.TrimSpace(scope.ConversationID)
	if conversationID == "" {
		conversationID = xid.New().String()
	}
	state := loadConversation(conversationID)
	maxContextTokens := s.cfg.Agent.MaxContextTokens
	if maxContextTokens <= 0 {
		maxContextTokens = 131072
	}
	reserveTokens := s.cfg.Agent.MaxOutputTokens
	if reserveTokens < 0 {
		reserveTokens = 0
	}
	contextReset := false
	if state.ContextTokens > 0 && state.ContextTokens >= maxContextTokens-reserveTokens {
		log.FromContext(ctx).Infof("Agent conversation %s reached context budget (%d/%d tokens); starting fresh context", conversationID, state.ContextTokens, maxContextTokens)
		state = conversationState{}
		contextReset = true
		_ = cache.Set(conversationCacheKey(conversationID), state, cache.DefaultTTL)
	}

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	timeout := s.cfg.Agent.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	tools, toolset, err := s.tools(scope)
	if err != nil {
		return "", err
	}
	reasoningLevel := thinkingLevel(s.cfg.Agent.ReasoningLevel)
	a, err := llmagent.New(llmagent.Config{
		Name:        appName,
		Model:       s.model,
		Description: "Searches BTTS messages and answers questions from search results.",
		Instruction: s.instruction(),
		Tools:       tools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			MaxOutputTokens: int32(s.cfg.Agent.MaxOutputTokens),
			ThinkingConfig:  &genai.ThinkingConfig{ThinkingLevel: reasoningLevel},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}
	r, err := runner.NewInMemory(appName, a)
	if err != nil {
		return "", fmt.Errorf("create agent runner: %w", err)
	}

	sessionID := xid.New().String()
	scopeText := fmt.Sprintf("Current time: %s\nAllowed chat IDs: %v\n", time.Now().Format(time.RFC3339), scope.AllowedChatIDs)
	if scope.CurrentChatID != 0 {
		scopeText += fmt.Sprintf("Current chat ID: %d\n", scope.CurrentChatID)
	}
	parts := []*genai.Part{genai.NewPartFromText(scopeText)}
	if historyText := formatConversation(state.Turns); historyText != "" {
		parts = append(parts, genai.NewPartFromText(historyText))
	}
	parts = append(parts, genai.NewPartFromText(query))
	for i, attachment := range attachments {
		if i >= 2 || len(attachment.Data) == 0 {
			break
		}
		mime := strings.TrimSpace(attachment.MIME)
		if mime == "" {
			mime = "image/jpeg"
		}
		parts = append(parts, genai.NewPartFromBytes(attachment.Data, mime))
	}
	userContent := genai.NewContentFromParts(parts, genai.RoleUser)
	var answer string
	contextTokens := 0
	for event, runErr := range r.Run(ctx, scope.UserID, sessionID, userContent, adkagent.RunConfig{
		MaxLLMCalls: s.cfg.Agent.MaxLLMCalls,
	}) {
		if runErr != nil {
			if errors.Is(runErr, adkagent.ErrLLMCallsLimitExceeded) {
				if fallback := toolset.fallbackAnswer(); fallback != "" {
					if contextReset {
						fallback = contextResetNotice() + fallback
					}
					rememberConversation(conversationID, query, fallback, contextTokens)
					return fallback, nil
				}
			}
			return "", fmt.Errorf("agent run: %w", runErr)
		}
		if event == nil {
			continue
		}
		if usage := event.LLMResponse.UsageMetadata; usage != nil {
			contextTokens = int(usage.TotalTokenCount)
			if contextTokens == 0 {
				contextTokens = int(usage.PromptTokenCount + usage.CandidatesTokenCount + usage.ToolUsePromptTokenCount + usage.ThoughtsTokenCount)
			}
		}
		if event.LLMResponse.ErrorMessage != "" {
			return "", fmt.Errorf("agent model: %s", event.LLMResponse.ErrorMessage)
		}
		if !event.IsFinalResponse() || event.LLMResponse.Content == nil {
			continue
		}
		var text strings.Builder
		for _, part := range event.LLMResponse.Content.Parts {
			if part != nil && !part.Thought {
				text.WriteString(part.Text)
			}
		}
		if strings.TrimSpace(text.String()) != "" {
			answer = text.String()
		}
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", errors.New("agent returned an empty answer")
	}
	maxAnswerChars := s.cfg.Agent.MaxAnswerChars
	if maxAnswerChars <= 0 {
		maxAnswerChars = 4000
	}
	answer = truncateRunes(answer, maxAnswerChars)
	if contextReset {
		answer = contextResetNotice() + answer
	}
	rememberConversation(conversationID, query, answer, contextTokens)
	return answer, nil
}

func contextResetNotice() string {
	return "(上下文已达到上限，已开始新的对话上下文。)\n\n"
}

type conversationTurn struct {
	Query  string
	Answer string
}

type conversationState struct {
	Turns         []conversationTurn
	ContextTokens int
}

func conversationCacheKey(conversationID string) string {
	return "agent-conversation:" + conversationID
}

func loadConversation(conversationID string) conversationState {
	state, ok := cache.Get[conversationState](conversationCacheKey(conversationID))
	if !ok {
		return conversationState{}
	}
	return state
}

func rememberConversation(conversationID, query, answer string, contextTokens int) {
	state := loadConversation(conversationID)
	state.Turns = append(state.Turns, conversationTurn{Query: query, Answer: answer})
	state.ContextTokens = contextTokens
	_ = cache.Set(conversationCacheKey(conversationID), state, cache.DefaultTTL)
}

func formatConversation(turns []conversationTurn) string {
	if len(turns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Previous conversation:\n")
	for _, turn := range turns {
		b.WriteString("User: ")
		b.WriteString(turn.Query)
		b.WriteString("\nAssistant: ")
		b.WriteString(turn.Answer)
		b.WriteString("\n")
	}
	return b.String()
}

func thinkingLevel(level string) genai.ThinkingLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "minimal":
		return genai.ThinkingLevelMinimal
	case "medium":
		return genai.ThinkingLevelMedium
	case "high":
		return genai.ThinkingLevelHigh
	default:
		return genai.ThinkingLevelLow
	}
}

func (s *Service) instruction() string {
	var b strings.Builder
	b.WriteString("You are the BTTS search agent, a ReAct-style assistant for a Telegram message index.\n")
	b.WriteString("Your job is to understand the user's search intent, choose bounded search parameters, call the provided tools, inspect the results (including photos, videos, audio, documents, OCR, tags, and extracted text), and answer from those results.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Only use the provided tools. Never invent messages, links, or facts.\n")
	b.WriteString("- If the user attached an image, use it together with the caption as the question. Still use the tools when searching the indexed chats.\n")
	b.WriteString("- Search only within the allowed chats. The tools enforce this boundary.\n")
	b.WriteString("- If the target chat is ambiguous, call list_chats first. If a query is ambiguous, run a few focused searches with different keywords and filters.\n")
	b.WriteString("- Use mode=keyword for exact terms, hybrid for normal semantic search, and media only for a text-to-media semantic search.\n")
	b.WriteString("- Use get_messages when a result needs more detail. Do not ask the user for a chat ID if it can be resolved from list_chats.\n")
	b.WriteString("- If get_messages returns media_artifacts, call load_artifacts with those artifact names before claiming to have inspected the media content.\n")
	b.WriteString("- Include the relevant message link(s) in the final answer. Answer in the user's language. Be concise, but explain uncertainty or lack of results.\n")
	b.WriteString("- For 'latest', 'recent', or 'newest' requests, set sort=newest. To list the newest photos without a keyword filter, call search_messages with query empty, mode keyword, types [\"photo\"], sort newest. For oldest, set sort=oldest. Do not claim a result is the latest without a newest-sorted search.\n")
	b.WriteString("- Media is only loaded when get_messages is called with include_media=true. Set it only when the user asks about visual or audio content, then call load_artifacts; do not load media for ordinary text searches.\n")
	b.WriteString("- Use at most 3 search_messages calls per request. If the first 1-2 searches return relevant hits, stop and answer; do not search again with minor synonyms.\n")
	b.WriteString("- Never call search_messages with a one-character query. Empty queries are only for the latest/oldest media listing above. Do not call list_chats when the scope above already identifies the allowed chats.\n")
	b.WriteString("- If searches return no useful results after at most 3 attempts, say so rather than looping.\n")
	b.WriteString("- Do not call tools after you have enough evidence; stop and answer. Return only the final user-facing answer; do not include your internal reasoning, search budget notes, or analysis.\n\n")
	return b.String()
}
