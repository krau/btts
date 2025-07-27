package utils

import (
	"github.com/gotd/td/telegram/downloader"
)

func NewDownloader(file TGFile) *downloader.Builder {
	return downloader.NewDownloader().WithPartSize(1024*1024).
		Download(file.Client(), file.Location())
}
