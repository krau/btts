package bot

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/types"
	"github.com/krau/btts/userclient"
	"github.com/krau/btts/utils"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
	"gorm.io/gorm"
)

// importLease is longer than any single batch/page operation. A process
// restart clears stale leases in cmd/run.go.
const importLease = time.Hour

// importSlots bounds concurrent history imports so Telegram and the indexer
// are not saturated by several /add commands.
var importSlots = make(chan struct{}, 1)

func AddHandler(ctx *ext.Context, update *ext.Update) error {
	return addHandler(ctx, update, false)
}

func AddTakeoutHandler(ctx *ext.Context, update *ext.Update) error {
	return addHandler(ctx, update, true)
}

func addHandler(ctx *ext.Context, update *ext.Update, forceTakeout bool) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /add <chat> [takeout] [noenrich]"), nil)
		return dispatcher.EndGroups
	}
	useTakeout := forceTakeout
	noEnrich := false
	for _, arg := range args[2:] {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "takeout":
			useTakeout = true
		case "noenrich", "no-enrich":
			noEnrich = true
		}
	}
	logger := log.FromContext(ctx)

	chatArg := args[1]
	var inputPeer tg.InputPeerClass
	utclient := bi.UserClient.TClient

	chatID, err := strconv.ParseInt(chatArg, 10, 64)
	if err != nil {
		effChat, err := utclient.CreateContext().ResolveUsername(strings.TrimPrefix(chatArg, "@"))
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Failed to resolve username: "+err.Error()), nil)
			return dispatcher.EndGroups
		}
		inputPeer = effChat.GetInputPeer()
		chatID = effChat.GetID()
	} else {
		inputPeer, err = utclient.CreateContext().ResolveInputPeerById(chatID)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Chat not found: "+err.Error()), nil)
			return dispatcher.EndGroups
		}
		chatID = inputPeerID(inputPeer)
	}
	if chatID == 0 {
		ctx.Reply(update, ext.ReplyTextString("Chat not found"), nil)
		return dispatcher.EndGroups
	}

	// Claim a slot before claiming an import generation, so a busy process
	// cannot leave a lease that blocks the next attempt.
	select {
	case importSlots <- struct{}{}:
	default:
		ctx.Reply(update, ext.ReplyTextString("Another history import is running; try again later"), nil)
		return dispatcher.EndGroups
	}
	var releaseOnce sync.Once
	releaseSlot := func() {
		releaseOnce.Do(func() { <-importSlots })
	}
	handedOff := false
	defer func() {
		if !handedOff {
			releaseSlot()
		}
	}()

	importMode := "raw"
	if useTakeout {
		importMode = "takeout"
	}
	imp, resuming, err := database.StartImport(ctx, chatID, importMode, importLease)
	switch {
	case errors.Is(err, database.ErrImportCompleted):
		ctx.Reply(update, ext.ReplyTextString("Chat already indexed"), nil)
		return dispatcher.EndGroups
	case errors.Is(err, database.ErrImportInProgress):
		ctx.Reply(update, ext.ReplyTextString("Import already in progress"), nil)
		return dispatcher.EndGroups
	case errors.Is(err, database.ErrImportModeMismatch):
		existing, getErr := database.GetImport(ctx, chatID)
		if getErr == nil && existing.Mode != "" {
			ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf(
				"Existing import uses %s mode; resume it with the same command", existing.Mode,
			)), nil)
		} else {
			ctx.Reply(update, ext.ReplyTextString("Existing import uses a different mode"), nil)
		}
		return dispatcher.EndGroups
	case err != nil:
		logger.Errorf("Read import state for %d: %v", chatID, err)
		ctx.Reply(update, ext.ReplyTextString("Failed to read import state: "+err.Error()), nil)
		return dispatcher.EndGroups
	}

	if err := bi.Engine.CreateIndex(ctx, chatID); err != nil {
		_ = database.StopImport(ctx, chatID, imp.Generation)
		ctx.Reply(update, ext.ReplyTextString("Failed to create index: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	if err := ensureIndexChat(ctx, chatID, inputPeer, noEnrich); err != nil {
		if delErr := bi.Engine.DeleteIndex(ctx, chatID); delErr != nil {
			logger.Errorf("Rollback index for %d: %v", chatID, delErr)
		}
		_ = database.StopImport(ctx, chatID, imp.Generation)
		ctx.Reply(update, ext.ReplyTextString("Failed to add chat: "+err.Error()), nil)
		return dispatcher.EndGroups
	}

	builder := query.Messages(utclient.API()).GetHistory(inputPeer).BatchSize(100)
	if imp.Cursor > 0 {
		builder = builder.OffsetID(imp.Cursor)
	}
	total, err := builder.Count(ctx)
	if err != nil {
		total = -1
		ctx.Reply(update, ext.ReplyTextString("Failed to count messages: "+err.Error()), nil)
	}
	if resuming {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf(
			"Resuming import from message %d (total history: %d)", imp.Cursor, total,
		)), nil)
	} else {
		ctx.Reply(update, ext.ReplyTextString("Total messages: "+strconv.Itoa(total)), nil)
	}

	delay := config.C.Import.BatchDelay
	enricher := bi.Engine.Enrichment()
	run := func(runCtx context.Context) {
		if err := database.RefreshImport(runCtx, chatID, imp.Generation); err != nil {
			logger.Warnf("Import chat %d superseded before start: %v", chatID, err)
			return
		}
		var processed, finalRange, finalCursor int
		var runErr error
		if useTakeout {
			processed, finalRange, finalCursor, runErr = importHistoryTakeout(
				runCtx, ctx, bi.UserClient.GetContext(), bi.UserClient, bi.Engine,
				inputPeer, chatID, imp.RangeIndex, imp.Cursor, imp.Generation, delay, enricher,
			)
		} else {
			processed, finalCursor, runErr = importHistory(
				runCtx, ctx, bi.UserClient.GetContext(), utclient, bi.Engine,
				inputPeer, chatID, imp.Cursor, imp.Generation, delay, enricher,
			)
			finalRange = 0
		}
		if runErr != nil {
			if tg.IsTakeoutInitDelay(runErr) {
				if markErr := database.StopImport(runCtx, chatID, imp.Generation); markErr != nil {
					logger.Warnf("Mark takeout delay for %d: %v", chatID, markErr)
				}
				ctx.Reply(update, ext.ReplyTextString("Takeout API is not available yet for this account: "+runErr.Error()), nil)
				return
			}
			if errors.Is(runErr, database.ErrImportSuperseded) {
				logger.Warnf("Import chat %d lost its generation: %v", chatID, runErr)
				return
			}
			if markErr := database.StopImport(runCtx, chatID, imp.Generation); markErr != nil {
				logger.Warnf("Mark import stopped for %d: %v", chatID, markErr)
			}
			logger.Errorf("Import chat %d stopped before cursor %d: %v", chatID, finalCursor, runErr)
			ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf(
				"Import stopped after %d messages; run /add again to resume: %v", processed, runErr,
			)), nil)
			return
		}
		if err := database.UpdateImportProgress(runCtx, chatID, imp.Generation, finalRange, finalCursor, true); err != nil {
			logger.Errorf("Complete import state for %d: %v", chatID, err)
			return
		}
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf(
			"Added %d messages from chat %s", processed, chatArg,
		)), nil)
	}

	if config.C.Import.Background {
		ctx.Reply(update, ext.ReplyTextString("History import started in background; media enrichment will continue after the command returns."), nil)
		handedOff = true
		go func() {
			defer releaseSlot()
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Errorf("Import panic for chat %d: %v", chatID, recovered)
				}
			}()
			run(context.WithoutCancel(ctx))
		}()
		return dispatcher.EndGroups
	}
	run(ctx)
	return dispatcher.EndGroups
}

// inputPeerID returns the plain MTProto ID carried by a resolved peer.
func inputPeerID(peer tg.InputPeerClass) int64 {
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		return p.UserID
	case *tg.InputPeerChat:
		return p.ChatID
	case *tg.InputPeerChannel:
		return p.ChannelID
	default:
		return 0
	}
}

func ensureIndexChat(ctx context.Context, chatID int64, inputPeer tg.InputPeerClass, noEnrich bool) error {
	if _, err := database.GetIndexChat(ctx, chatID); err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	indexChat := &database.IndexChat{ChatID: chatID, Watching: true, NoEnrich: noEnrich}
	switch inputPeer.(type) {
	case *tg.InputPeerChannel:
		indexChat.Type = int(database.ChatTypeChannel)
	case *tg.InputPeerUser:
		indexChat.Type = int(database.ChatTypePrivate)
	default:
		return fmt.Errorf("unsupported chat type %T", inputPeer)
	}
	return database.UpsertIndexChat(ctx, indexChat)
}

type documentAdder interface {
	AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error
}

// importHistory walks the chat from newest to oldest. The returned cursor is
// the ID of the last message successfully written to the main index.
func importHistory(
	ctx context.Context,
	extCtx *ext.Context,
	mediaCtx *ext.Context,
	utclient interface {
		API() *tg.Client
	},
	adder documentAdder,
	inputPeer tg.InputPeerClass,
	chatID int64,
	cursor int,
	generation int64,
	batchDelay time.Duration,
	enricher types.MediaEnricher,
) (int, int, error) {
	builder := query.Messages(utclient.API()).GetHistory(inputPeer).BatchSize(100)
	if cursor > 0 {
		builder = builder.OffsetID(cursor)
	}
	iter := builder.Iter()
	logger := log.FromContext(ctx)

	batch := make([]*tg.Message, 0, 100)
	processed := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := database.RefreshImport(ctx, chatID, generation); err != nil {
			return err
		}
		docs := engine.DocumentsFromMessages(ctx, batch, chatID, extCtx.Self.ID, mediaCtx, false, enricher)
		engine.MarkImported(docs)
		var addErr error
		for attempt := range 3 {
			if addErr = adder.AddDocuments(ctx, chatID, docs); addErr == nil {
				break
			}
			logger.Warnf("Index batch for chat %d failed (attempt %d): %v", chatID, attempt+1, addErr)
			if err := utils.SleepContext(ctx, time.Duration(attempt+1)*3*time.Second); err != nil {
				return err
			}
		}
		if addErr != nil {
			return addErr
		}
		cursor = batch[len(batch)-1].ID
		if err := database.UpdateImportProgress(ctx, chatID, generation, 0, cursor, false); err != nil {
			return fmt.Errorf("save import cursor: %w", err)
		}
		processed += len(batch)
		batch = batch[:0]
		return nil
	}

	for iter.Next(ctx) {
		value := iter.Value()
		msg, ok := value.Msg.(*tg.Message)
		if !ok {
			continue
		}
		batch = append(batch, msg)
		if len(batch) >= 100 {
			if err := flush(); err != nil {
				return processed, cursor, err
			}
			if err := utils.SleepContext(ctx, batchDelay); err != nil {
				return processed, cursor, err
			}
		}
	}
	if err := iter.Err(); err != nil {
		return processed, cursor, err
	}
	if err := flush(); err != nil {
		return processed, cursor, err
	}
	return processed, cursor, nil
}

func takeoutMediaDelay() time.Duration {
	rps := config.C.Enrich.MediaFetchPerSec
	if rps <= 0 {
		return 0
	}
	delay := time.Duration(float64(time.Second) / rps)
	if delay <= 0 {
		return 0
	}
	jitter := time.Duration(rand.Int64N(int64(delay/5) + 1))
	return delay + jitter
}

func takeoutPause(ctx context.Context) error {
	return utils.SleepContext(ctx, takeoutMediaDelay())
}

type takeoutImporter interface {
	TakeoutImportChat(
		ctx context.Context,
		inputPeer tg.InputPeerClass,
		chatID int64,
		startRange, startCursor int,
		handler func(context.Context, userclient.TakeoutChatPage) error,
	) error
}

// importHistoryTakeout exports one chat through a Telegram takeout session.
// It stores per-split-range progress so a session expiry or restart resumes
// at the correct range and offset.
func importHistoryTakeout(
	ctx context.Context,
	extCtx *ext.Context,
	mediaCtx *ext.Context,
	importer takeoutImporter,
	adder documentAdder,
	inputPeer tg.InputPeerClass,
	chatID int64,
	startRange, startCursor int,
	generation int64,
	batchDelay time.Duration,
	enricher types.MediaEnricher,
) (int, int, int, error) {
	logger := log.FromContext(ctx)
	maxBytes := int64(config.C.Enrich.MaxDownloadMB) << 20
	processed := 0
	finalRange := startRange
	finalCursor := startCursor
	handler := func(ctx context.Context, page userclient.TakeoutChatPage) error {
		if err := database.RefreshImport(ctx, chatID, generation); err != nil {
			return err
		}
		if len(page.Messages) > 0 {
			docs := engine.DocumentsFromMessages(ctx, page.Messages, chatID, extCtx.Self.ID, mediaCtx, false, enricher)
			engine.MarkImported(docs)

			addDocuments := adder.AddDocuments
			if enricher != nil {
				addDocuments = enricher.AddDocumentsOnly
			}
			var addErr error
			for attempt := range 3 {
				if addErr = addDocuments(ctx, chatID, docs); addErr == nil {
					break
				}
				logger.Warnf("Index takeout batch for chat %d failed (attempt %d): %v", chatID, attempt+1, addErr)
				if err := utils.SleepContext(ctx, time.Duration(attempt+1)*3*time.Second); err != nil {
					return err
				}
			}
			if addErr != nil {
				return addErr
			}
			processed += len(page.Messages)

			if enricher != nil && page.Download != nil {
				byID := make(map[int64]*tg.Message, len(page.Messages))
				for _, msg := range page.Messages {
					byID[int64(msg.ID)] = msg
				}
				for _, doc := range docs {
					if doc == nil || !doc.HasMedia || !enricher.NeedsEnrichment(chatID, doc) {
						continue
					}
					msg := byID[doc.ID]
					if msg == nil {
						continue
					}
					media, ok := msg.GetMedia()
					if !ok {
						continue
					}
					if size := engine.MediaSize(media); size > maxBytes {
						logger.Debugf("Skipping oversized takeout media %d_%d: %d bytes", chatID, doc.ID, size)
						continue
					}
					buf := &utils.CappedBuffer{Limit: maxBytes}
					if err := page.Download(ctx, media, buf); err != nil {
						if errors.Is(err, utils.ErrMediaTooLarge) {
							logger.Debugf("Skipping oversized takeout media %d_%d: %v", chatID, doc.ID, err)
							continue
						}
						logger.Warnf("Takeout media %d_%d failed, queueing fallback: %v", chatID, doc.ID, err)
						if fallbackErr := enricher.EnqueueDocument(ctx, chatID, doc); fallbackErr != nil {
							return fmt.Errorf("enqueue fallback media %d_%d: %w", chatID, doc.ID, fallbackErr)
						}
						if err := takeoutPause(ctx); err != nil {
							return err
						}
						continue
					}
					if err := enricher.EnrichNow(ctx, chatID, doc, buf.Bytes(), doc.MediaMIME); err != nil {
						logger.Warnf("Enrich takeout media %d_%d failed, queueing fallback: %v", chatID, doc.ID, err)
						if fallbackErr := enricher.EnqueueDocument(ctx, chatID, doc); fallbackErr != nil {
							return fmt.Errorf("enqueue fallback media %d_%d: %w", chatID, doc.ID, fallbackErr)
						}
						if err := takeoutPause(ctx); err != nil {
							return err
						}
						continue
					}
					if err := takeoutPause(ctx); err != nil {
						return err
					}
				}
			}
		}
		finalRange, finalCursor = page.Range, page.Cursor
		if err := database.UpdateImportProgress(ctx, chatID, generation, page.Range, page.Cursor, false); err != nil {
			return fmt.Errorf("save takeout cursor: %w", err)
		}
		if len(page.Messages) > 0 {
			return utils.SleepContext(ctx, batchDelay)
		}
		return nil
	}
	if err := importer.TakeoutImportChat(ctx, inputPeer, chatID, startRange, startCursor, handler); err != nil {
		return processed, finalRange, finalCursor, err
	}
	return processed, finalRange, finalCursor, nil
}
