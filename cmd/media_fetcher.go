package cmd

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/krau/btts/config"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/userclient"
	"github.com/krau/btts/utils"
	"github.com/krau/mygotg/ext"
)

type rateGate struct {
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

func (g *rateGate) wait(ctx context.Context) error {
	if g == nil || g.interval <= 0 {
		return nil
	}
	g.mu.Lock()
	now := time.Now()
	if g.next.Before(now) {
		g.next = now
	}
	wait := g.next.Sub(now)
	g.next = g.next.Add(g.interval)
	if jitter := time.Duration(rand.Int64N(int64(g.interval/5) + 1)); jitter > 0 {
		g.next = g.next.Add(jitter)
	}
	g.mu.Unlock()
	return utils.SleepContext(ctx, wait)
}

type telegramMediaFetcher struct {
	client   *userclient.UserClient
	maxBytes int64
	gate     *rateGate
	sem      chan struct{}
	retries  int
	extra    time.Duration
}

func newTelegramMediaFetcher(client *userclient.UserClient, cfg config.AppConfig) *telegramMediaFetcher {
	var gate *rateGate
	if cfg.Enrich.MediaFetchPerSec > 0 {
		gate = &rateGate{interval: time.Duration(float64(time.Second) / cfg.Enrich.MediaFetchPerSec)}
	}
	return &telegramMediaFetcher{
		client:   client,
		maxBytes: int64(cfg.Enrich.MaxDownloadMB) << 20,
		gate:     gate,
		sem:      make(chan struct{}, cfg.Enrich.MediaFetchConcurrency),
		retries:  cfg.Enrich.MediaFetchRetries,
		extra:    cfg.Enrich.FloodWaitExtra,
	}
}

func (f *telegramMediaFetcher) Fetch(ctx context.Context, chatID int64, messageID int, mimeHint string) ([]byte, string, error) {
	select {
	case f.sem <- struct{}{}:
		defer func() { <-f.sem }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	if err := f.gate.wait(ctx); err != nil {
		return nil, "", err
	}
	var lastErr error
	for attempt := 0; attempt <= f.retries; attempt++ {
		data, mime, err := f.fetchOnce(ctx, chatID, messageID, mimeHint)
		if err == nil {
			return data, mime, nil
		}
		lastErr = err
		wait, ok := tgerr.AsFloodWait(err)
		if !ok {
			break
		}
		sleep := wait + f.extra
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, "", ctx.Err()
		case <-timer.C:
		}
	}
	return nil, "", lastErr
}

func (f *telegramMediaFetcher) fetchOnce(ctx context.Context, chatID int64, messageID int, mimeHint string) ([]byte, string, error) {
	ectx := f.client.GetContext()
	msgs, err := ectx.GetMessages(chatID, []tg.InputMessageClass{
		&tg.InputMessageID{ID: messageID},
	})
	if err != nil {
		return nil, "", fmt.Errorf("get message: %w", err)
	}
	if len(msgs) == 0 {
		return nil, "", fmt.Errorf("message %d not found", messageID)
	}
	msg, ok := msgs[0].(*tg.Message)
	if !ok {
		return nil, "", fmt.Errorf("message %d is not a regular message", messageID)
	}
	media, ok := msg.GetMedia()
	if !ok {
		return nil, "", fmt.Errorf("message %d has no media", messageID)
	}
	if size := engine.MediaSize(media); size > f.maxBytes {
		return nil, "", fmt.Errorf("%w: %d bytes (limit %d)", utils.ErrMediaTooLarge, size, f.maxBytes)
	}
	mime := mimeHint
	if mime == "" {
		mime, _ = engine.MediaMeta(media)
	}
	buf := &utils.CappedBuffer{Limit: f.maxBytes}
	if _, err := ectx.DownloadMedia(media, ext.DownloadOutputStream{Writer: buf}, nil); err != nil {
		return nil, "", fmt.Errorf("download media: %w", err)
	}
	if buf.Len() == 0 {
		return nil, "", fmt.Errorf("downloaded media is empty")
	}
	return buf.Bytes(), mime, nil
}
