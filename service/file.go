package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/bot"
	"github.com/krau/btts/config"
	"github.com/krau/btts/userclient"
	"github.com/krau/btts/utils"
)

type TGFileFileReader struct {
	RC       io.ReadCloser
	Size     int64
	Name     string
	FilePath string // set when backed by a disk cache file
}

func (r *TGFileFileReader) Read(p []byte) (n int, err error) {
	return r.RC.Read(p)
}

func (r *TGFileFileReader) Close() error {
	return r.RC.Close()
}

func GetTGFileReader(ctx context.Context, chatID int64, messageId int) (*TGFileFileReader, error) {
	logger := log.FromContext(ctx)

	// Try disk cache first
	if config.C.FileCache.Disable {
		initFileCache()
		cached, err := getCachedFileReader(chatID, messageId)
		if err == nil && cached != nil {
			logger.Info("File cache hit", "chat_id", chatID, "message_id", messageId)
			return cached, nil
		}
	}

	ectx := bot.GetBot().GetContext()
	msg, err := utils.GetMessageByID(ectx, chatID, messageId)
	if err != nil || msg == nil {
		ectx = userclient.GetUserClient().GetContext()
		msg, err = utils.GetMessageByID(ectx, chatID, messageId)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message %d in chat %d: %w", messageId, chatID, err)
	}
	if msg.Media == nil {
		return nil, fmt.Errorf("message %d in chat %d has no media", messageId, chatID)
	}

	media := msg.Media

	tf, err := utils.FileFromMedia(media, ectx.Raw)
	if err != nil {
		return nil, fmt.Errorf("failed to get file from media: %w", err)
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_, err = utils.NewDownloader(tf).Stream(ctx, pw)
		if err != nil && err != io.EOF {
			if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.ErrClosedPipe) {
				return
			}
			logger.Error("failed to download file", "chat_id", chatID, "message_id", messageId, "error", err)
			pw.CloseWithError(fmt.Errorf("failed to download file: %w", err))
			return
		}
	}()
	result := &TGFileFileReader{
		RC:   pr,
		Size: tf.Size(),
		Name: tf.Name(),
	}

	// Wrap with disk cache if enabled
	if config.C.FileCache.Disable {
		initFileCache()
		result = wrapWithCache(ctx, result, chatID, messageId)
	}

	return result, nil
}
