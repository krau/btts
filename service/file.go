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
	return GetTGFileReaderWithSizeLimit(ctx, chatID, messageId, 0)
}

// GetTGFileReaderWithSizeLimit rejects known oversized files before streaming.
// A non-positive maxBytes disables the check; callers must still bound reads of unknown sizes.
func GetTGFileReaderWithSizeLimit(ctx context.Context, chatID int64, messageId int, maxBytes int64) (*TGFileFileReader, error) {
	logger := log.FromContext(ctx)

	// Try disk cache first
	if config.C.FileCache.Disable {
		initFileCache()
		cached, err := getCachedFileReader(chatID, messageId)
		if err == nil && cached != nil {
			if err := checkTGFileSize(cached.Size, maxBytes); err != nil {
				_ = cached.Close()
				return nil, err
			}
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
	result, err := newTGFileStream(ctx, tf, maxBytes, logger.With("chat_id", chatID, "message_id", messageId))
	if err != nil {
		return nil, err
	}

	// Wrap with disk cache if enabled
	if config.C.FileCache.Disable {
		initFileCache()
		result = wrapWithCache(ctx, result, chatID, messageId)
	}

	return result, nil
}

func checkTGFileSize(size, maxBytes int64) error {
	if maxBytes > 0 && size > maxBytes {
		return fmt.Errorf("%w: %d bytes (limit %d)", utils.ErrMediaTooLarge, size, maxBytes)
	}
	return nil
}

func newTGFileStream(ctx context.Context, file utils.TGFile, maxBytes int64, logger *log.Logger) (*TGFileFileReader, error) {
	size := file.Size()
	if err := checkTGFileSize(size, maxBytes); err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_, err := utils.NewDownloader(file).Stream(ctx, pw)
		if err != nil && err != io.EOF {
			if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.ErrClosedPipe) {
				return
			}
			logger.Error("failed to download file", "error", err)
			pw.CloseWithError(fmt.Errorf("failed to download file: %w", err))
			return
		}
	}()
	return &TGFileFileReader{
		RC:   pr,
		Size: size,
		Name: file.Name(),
	}, nil
}
