package cmd

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"

	"github.com/krau/btts/service"
)

const maxAgentMediaBytes = 20 << 20

type agentMediaLoader struct{}

func (agentMediaLoader) LoadMedia(ctx context.Context, chatID int64, messageID int) ([]byte, string, string, error) {
	reader, err := service.GetTGFileReader(ctx, chatID, messageID)
	if err != nil {
		return nil, "", "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxAgentMediaBytes+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(data) > maxAgentMediaBytes {
		return nil, "", "", fmt.Errorf("media exceeds %d bytes", maxAgentMediaBytes)
	}
	mimeType := mime.TypeByExtension(filepath.Ext(reader.Name))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, reader.Name, nil
}
