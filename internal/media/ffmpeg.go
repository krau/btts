package media

import (
	"os"
	"os/exec"
	"sync"
)

var ffmpegPath = sync.OnceValues(func() (string, error) {
	return exec.LookPath("ffmpeg")
})

func FFmpegPath() (string, error) {
	return ffmpegPath()
}

// WriteTempFile stores media bytes in a seekable temp file. MP4 and other
// container formats may keep their index at the end and cannot be demuxed
// reliably from a pipe.
func WriteTempFile(data []byte) (path string, cleanup func(), err error) {
	file, err := os.CreateTemp("", "btts-media-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.Remove(file.Name()) }
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return file.Name(), cleanup, nil
}
