package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/bot"
	"github.com/krau/btts/userclient"
	"github.com/krau/btts/utils"
)

type TGFileFileReader struct {
	RC   io.ReadCloser
	Size int64
	Name string
}

func (r *TGFileFileReader) Read(p []byte) (n int, err error) {
	return r.RC.Read(p)
}

func (r *TGFileFileReader) Close() error {
	return r.RC.Close()
}

func GetTGFileReader(ctx context.Context, chatID int64, messageId int) (*TGFileFileReader, error) {
	logger := log.FromContext(ctx)
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
			if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
				return
			}
			logger.Error("failed to download file", "chat_id", chatID, "message_id", messageId, "error", err)
			pw.CloseWithError(fmt.Errorf("failed to download file: %w", err))
			return
		}
	}()
	return &TGFileFileReader{
		RC:   pr,
		Size: tf.Size(),
		Name: tf.Name(),
	}, nil
}
