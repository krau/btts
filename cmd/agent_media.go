package cmd

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"

	"github.com/krau/btts/service"
	"github.com/krau/btts/utils"
)

const maxAgentMediaBytes = 20 << 20

type agentMediaLoader struct{}

func (agentMediaLoader) LoadMedia(ctx context.Context, chatID int64, messageID int) ([]byte, string, string, error) {
	reader, err := service.GetTGFileReaderWithSizeLimit(ctx, chatID, messageID, maxAgentMediaBytes)
	if err != nil {
		return nil, "", "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxAgentMediaBytes+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(data) > maxAgentMediaBytes {
		return nil, "", "", fmt.Errorf("%w: data exceeds %d bytes", utils.ErrMediaTooLarge, maxAgentMediaBytes)
	}
	mimeType := mime.TypeByExtension(filepath.Ext(reader.Name))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, reader.Name, nil
}
