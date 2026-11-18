package service

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"

	"github.com/krau/btts/utils"
)

func TestTGFileStreamSizeLimit(t *testing.T) {
	const limit = int64(5)
	for _, tc := range []struct {
		name     string
		size     int64
		maxBytes int64
		tooLarge bool
	}{
		{name: "oversized", size: limit + 1, maxBytes: limit, tooLarge: true},
		{name: "at limit", size: limit, maxBytes: limit},
		{name: "unknown size", maxBytes: limit},
		{name: "unlimited", size: limit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := &fileDownloadStub{}
				file := utils.NewTGFile(&tg.InputDocumentFileLocation{ID: 1}, client, tc.size, "video.mp4")
				reader, err := newTGFileStream(t.Context(), file, tc.maxBytes, log.Default())
				if reader != nil {
					defer reader.Close()
				}
				if tc.tooLarge {
					synctest.Wait()
					if reader != nil || !errors.Is(err, utils.ErrMediaTooLarge) || client.requests != 0 {
						t.Fatalf("oversized media: reader=%v, err=%v, download requests=%d", reader, err, client.requests)
					}
					return
				}
				if err != nil || reader == nil {
					t.Fatalf("open stream: reader=%v, err=%v", reader, err)
				}
				data, err := io.ReadAll(reader)
				if err != nil || string(data) != "video" {
					t.Fatalf("read stream: data=%q, err=%v", data, err)
				}
			})
		})
	}
}

type fileDownloadStub struct {
	downloader.Client
	requests int
}

func (s *fileDownloadStub) UploadGetFile(context.Context, *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	s.requests++
	return &tg.UploadFile{Type: &tg.StorageFileMp4{}, Bytes: []byte("video")}, nil
}
