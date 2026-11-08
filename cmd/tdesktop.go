package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/constant"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/service/enrich"
	"github.com/krau/btts/tdesktop"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils"
	"github.com/spf13/cobra"
	"gorm.io/gorm"
)

type tdesktopOptions struct {
	noWatch        bool
	noMedia        bool
	mediaWorkers   int
	mediaBatchSize int
}

func RegisterTDesktopCmd(root *cobra.Command) {
	var opts tdesktopOptions
	cmd := &cobra.Command{
		Use:   "import-tdesktop <result.json-or-export-dir>",
		Short: "Import a Telegram Desktop JSON export into the index",
		Long: `Import a Telegram Desktop JSON export without talking to Telegram.
The path may point to result.json or to the export directory. Both single-chat
and full-account exports are supported. Local media files are enriched inline
when enrichment is enabled; use --no-media to index text only. Imported chats
are watched by default; use --nowatch to keep their existing watch state.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTDesktopImport(cmd.Context(), args[0], opts)
		},
	}
	cmd.Flags().BoolVar(&opts.noWatch, "nowatch", false, "Do not enable watching for imported chats")
	cmd.Flags().BoolVar(&opts.noMedia, "no-media", false, "Index messages only; do not read or enrich local media")
	cmd.Flags().IntVar(&opts.mediaWorkers, "media-workers", 1, "Concurrent offline media workers")
	cmd.Flags().IntVar(&opts.mediaBatchSize, "media-batch-size", 4, "Images per batch (max 16)")
	root.AddCommand(cmd)
}

func runTDesktopImport(parent context.Context, exportPath string, opts tdesktopOptions) error {
	logger := log.NewWithOptions(os.Stdout, log.Options{
		Level:           log.InfoLevel,
		ReportTimestamp: true,
		TimeFormat:      time.TimeOnly,
	})
	ctx := log.WithContext(parent, logger)
	config.Init()
	if opts.mediaWorkers <= 0 {
		opts.mediaWorkers = 1
	}
	if opts.mediaWorkers > 16 {
		opts.mediaWorkers = 16
	}
	if opts.mediaBatchSize <= 0 {
		opts.mediaBatchSize = 4
	}
	if opts.mediaBatchSize > 16 {
		opts.mediaBatchSize = 16
	}
	if err := os.MkdirAll("data", os.ModePerm); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := database.InitDatabase(ctx); err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	logger.Infof("Loading Telegram Desktop export from %s", exportPath)
	chats, err := tdesktop.Load(exportPath)
	if err != nil {
		return err
	}
	messageCount := 0
	for _, chat := range chats {
		messageCount += len(chat.Messages)
	}
	logger.Infof("Loaded %d chats with %d messages", len(chats), messageCount)
	if !opts.noMedia {
		logger.Infof("Offline media workers: %d, batch size: %d", opts.mediaWorkers, opts.mediaBatchSize)
	}
	rootDir := filepath.Dir(exportPath)
	if info, err := os.Stat(exportPath); err == nil && info.IsDir() {
		rootDir = exportPath
	}

	searcher, err := engine.NewEngine(ctx)
	if err != nil {
		return fmt.Errorf("initialize search engine: %w", err)
	}
	var enricher types.MediaEnricher
	if !opts.noMedia {
		wrapped, wrapErr := enrich.WrapForImport(ctx, searcher)
		if wrapErr != nil {
			logger.Warnf("Offline enrichment unavailable, indexing text only: %v", wrapErr)
		} else {
			searcher = wrapped
			enricher = searcher.Enrichment()
		}
	}
	engine.SetEngine(searcher)

	totalDocs, totalMedia := 0, 0
	for i, chat := range chats {
		if chat.ID == 0 {
			logger.Warnf("Skipping chat %q without an id", chat.NameForDisplay())
			continue
		}
		logger.Infof("Importing chat %d/%d: %s (export id=%d)", i+1, len(chats), chat.NameForDisplay(), chat.ID)
		docs, media, err := importTDesktopChat(ctx, logger, searcher, enricher, rootDir, chat, opts)
		if err != nil {
			return fmt.Errorf("import chat %s: %w", chat.NameForDisplay(), err)
		}
		totalDocs += docs
		totalMedia += media
	}
	logger.Infof("Telegram Desktop import finished: chats=%d messages=%d media=%d", len(chats), totalDocs, totalMedia)
	return nil
}

func importTDesktopChat(ctx context.Context, logger *log.Logger, searcher engine.Searcher, enricher types.MediaEnricher, rootDir string, chat tdesktop.Chat, opts tdesktopOptions) (int, int, error) {
	chatID := normalizeTDesktopID(chat.ID)
	chatType, public := tdesktopChatType(chat.Type)
	indexChat, err := database.GetIndexChat(ctx, chatID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, err
	}
	isNew := indexChat == nil
	watching := !opts.noWatch
	if isNew {
		indexChat = &database.IndexChat{ChatID: chatID, Watching: watching}
	}
	indexChat.Title = chat.NameForDisplay()
	indexChat.Type = chatType
	indexChat.Public = public
	if !opts.noWatch {
		indexChat.Watching = true
	}
	if err := searcher.CreateIndex(ctx, chatID); err != nil {
		return 0, 0, fmt.Errorf("create index: %w", err)
	}
	if err := database.UpsertIndexChat(ctx, indexChat); err != nil {
		return 0, 0, fmt.Errorf("save chat metadata: %w", err)
	}

	type pendingMedia struct {
		doc  *types.MessageDocument
		path string
		mime string
	}
	docs := make([]*types.MessageDocument, 0, len(chat.Messages))
	pending := make([]pendingMedia, 0)
	users := make(map[int64]string)
	for _, msg := range chat.Messages {
		if strings.EqualFold(msg.Type, "service") || msg.ID == 0 {
			continue
		}
		text := strings.TrimSpace(msg.PlainText())
		mediaPath, mimeType, mediaName, msgType, hasMedia := msg.Media()
		resolvedPath := ""
		enrichable := false
		if hasMedia && enricher != nil && mimeType != "" && !database.IsEnrichDisabled(chatID) {
			if resolvedPath, err = resolveTDesktopMedia(rootDir, mediaPath); err != nil {
				logger.Debugf("Skip media %s: %v", mediaPath, err)
			} else {
				enrichable = enricher.EnrichableMedia(mimeType, mediaName, msgType)
				if !enrichable {
					resolvedPath = ""
				}
			}
		}
		if text == "" && !enrichable {
			continue
		}
		if !hasMedia {
			msgType = types.MessageTypeText
		}
		doc := &types.MessageDocument{
			ID:        int64(msg.ID),
			Message:   text,
			Type:      int(msgType),
			UserID:    msg.UserID(chatID),
			ChatID:    chatID,
			Timestamp: msg.Timestamp(),
			HasMedia:  enrichable,
			MediaMIME: mimeType,
			MediaName: mediaName,
			Priority:  1,
		}
		docs = append(docs, doc)
		if enrichable {
			pending = append(pending, pendingMedia{doc: doc, path: resolvedPath, mime: mimeType})
		}
		if userID, ok := msg.FromUserID(); ok && strings.TrimSpace(msg.From) != "" {
			users[userID] = msg.From
		}
	}
	logger.Infof("  prepared %d searchable messages, %d media files", len(docs), len(pending))
	for userID, fullName := range users {
		firstName, lastName := splitTDesktopName(fullName)
		if err := database.UpsertUserInfo(ctx, &database.UserInfo{
			ChatID: userID, FirstName: firstName, LastName: lastName,
		}); err != nil {
			logger.Warnf("Save user %d: %v", userID, err)
		}
	}

	const batchSize = 100
	addBatch := func(batch []*types.MessageDocument) error {
		if len(batch) == 0 {
			return nil
		}
		if enricher != nil {
			return enricher.AddDocumentsOnly(ctx, chatID, batch)
		}
		return searcher.AddDocuments(ctx, chatID, batch)
	}
	for start := 0; start < len(docs); start += batchSize {
		end := min(start+batchSize, len(docs))
		if err := addBatch(docs[start:end]); err != nil {
			return 0, 0, fmt.Errorf("index messages: %w", err)
		}
		if end == len(docs) || end%1000 == 0 {
			logger.Infof("  indexed messages %d/%d", end, len(docs))
		}
		if end < len(docs) {
			if err := utils.SleepContext(ctx, config.C.Import.BatchDelay); err != nil {
				return 0, 0, err
			}
		}
	}

	type mediaTask struct {
		items []pendingMedia
		batch bool
	}
	maxBytes := int64(config.C.Enrich.MaxDownloadMB) << 20
	imageItems := make([]pendingMedia, 0)
	otherItems := make([]pendingMedia, 0)
	batchEnricher, canBatch := enricher.(types.ImageBatchEnricher)
	for _, item := range pending {
		if canBatch && item.doc.Type == int(types.MessageTypePhoto) && types.IsSupportedImage(item.mime, item.doc.MediaName) {
			imageItems = append(imageItems, item)
		} else {
			otherItems = append(otherItems, item)
		}
	}

	imageBatchSize := opts.mediaBatchSize
	if imageBatchSize <= 0 {
		imageBatchSize = 4
	}
	tasks := make([]mediaTask, 0, len(otherItems)+len(imageItems)/imageBatchSize+1)
	for start := 0; start < len(imageItems); start += imageBatchSize {
		end := min(start+imageBatchSize, len(imageItems))
		items := imageItems[start:end]
		tasks = append(tasks, mediaTask{items: items, batch: len(items) > 1})
	}
	for _, item := range otherItems {
		tasks = append(tasks, mediaTask{items: []pendingMedia{item}})
	}

	var processed atomic.Int64
	var enriched atomic.Int64
	var progressMu sync.Mutex
	lastProgress := 0
	logProgress := func() {
		if len(pending) < 10 {
			return
		}
		progressMu.Lock()
		defer progressMu.Unlock()
		current := int(processed.Load())
		if current == len(pending) || current-lastProgress >= 10 {
			logger.Infof("  enriched media %d/%d (success=%d)", current, len(pending), enriched.Load())
			lastProgress = current
		}
	}

	processImages := func(items []pendingMedia) {
		docs := make([]*types.MessageDocument, 0, len(items))
		images := make([][]byte, 0, len(items))
		for _, item := range items {
			data, err := readTDesktopMedia(item.path, maxBytes)
			if err != nil {
				logger.Debugf("Skip media %s: %v", item.path, err)
				continue
			}
			docs = append(docs, item.doc)
			images = append(images, data)
		}
		if len(docs) > 0 {
			if err := batchEnricher.EnrichImagesNow(ctx, chatID, docs, images); err != nil {
				logger.Warnf("Batch image enrichment failed, falling back to single requests: %v", err)
				for i, doc := range docs {
					if err := enricher.EnrichNow(ctx, chatID, doc, images[i], doc.MediaMIME); err != nil {
						logger.Warnf("Enrich image %s: %v", doc.MediaName, err)
						continue
					}
					enriched.Add(1)
				}
			} else {
				enriched.Add(int64(len(docs)))
			}
		}
	}

	processOne := func(item pendingMedia) {
		data, err := readTDesktopMedia(item.path, maxBytes)
		if err != nil {
			logger.Debugf("Skip media %s: %v", item.path, err)
			return
		}
		if err := enricher.EnrichNow(ctx, chatID, item.doc, data, item.mime); err != nil {
			logger.Warnf("Enrich media %s: %v", item.path, err)
			return
		}
		enriched.Add(1)
	}

	workerCount := min(opts.mediaWorkers, len(tasks))
	if workerCount < 1 && len(tasks) > 0 {
		workerCount = 1
	}
	if len(tasks) > 0 {
		logger.Infof("  enriching %d media files in %d tasks with %d workers", len(pending), len(tasks), workerCount)
		jobs := make(chan mediaTask)
		var wg sync.WaitGroup
		for range workerCount {
			wg.Go(func() {
				for task := range jobs {
					if task.batch {
						processImages(task.items)
					} else {
						processOne(task.items[0])
					}
					processed.Add(int64(len(task.items)))
					logProgress()
				}
			})
		}
		for _, task := range tasks {
			select {
			case jobs <- task:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return 0, 0, ctx.Err()
			}
		}
		close(jobs)
		wg.Wait()
	}
	mediaCount := int(enriched.Load())
	logger.Infof("Imported %s: chat_id=%d messages=%d media=%d", chat.NameForDisplay(), chatID, len(docs), mediaCount)
	return len(docs), mediaCount, nil
}

func resolveTDesktopMedia(rootDir, relative string) (string, error) {
	relative = strings.TrimSpace(strings.ReplaceAll(relative, "\\", "/"))
	if relative == "" {
		return "", errors.New("empty media path")
	}
	if filepath.IsAbs(relative) || strings.HasPrefix(relative, "file://") {
		return "", errors.New("absolute media paths are not supported")
	}
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(relative)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("media path escapes export directory")
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("media path is not a regular file")
	}
	return candidate, nil
}

func readTDesktopMedia(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, fmt.Errorf("%w: %d bytes", utils.ErrMediaTooLarge, info.Size())
	}
	return os.ReadFile(path)
}

func splitTDesktopName(name string) (string, string) {
	fields := strings.Fields(name)
	switch len(fields) {
	case 0:
		return "", ""
	case 1:
		return fields[0], ""
	default:
		return fields[0], strings.Join(fields[1:], " ")
	}
}

func normalizeTDesktopID(id int64) int64 {
	if id >= 0 {
		return id
	}
	if plain := int64(constant.TDLibPeerID(id).ToPlain()); plain > 0 {
		return plain
	}
	return id
}

func tdesktopChatType(value string) (int, bool) {
	switch strings.ToLower(value) {
	case "private_supergroup", "public_supergroup", "private_channel", "public_channel":
		return int(database.ChatTypeChannel), strings.HasPrefix(strings.ToLower(value), "public_")
	case "private_group":
		return int(database.ChatTypeGroup), false
	default:
		return int(database.ChatTypePrivate), false
	}
}
