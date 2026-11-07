package enrich

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"os/exec"
	"strconv"
	"strings"
)

const (
	videoFrameIntervalSec = 10
	videoFrameWidth       = 512
	maxVideoFrames        = 8
)

type videoFrame struct {
	data []byte
	at   float64
}

// videoFrames extracts up to count JPEG keyframes at 10-second intervals.
// Frames are scaled to at most 512 pixels wide before embedding.
func videoFrames(ctx context.Context, data []byte, count int) ([]videoFrame, error) {
	ffmpeg, err := findFFmpeg()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is required for video enrichment: %w", err)
	}
	if count <= 0 {
		count = 3
	}
	if count > maxVideoFrames {
		count = maxVideoFrames
	}
	path, cleanup, err := writeTempMedia(data)
	if err != nil {
		return nil, fmt.Errorf("write video temp file: %w", err)
	}
	defer cleanup()
	args := []string{
		"-hide_banner", "-loglevel", "error", "-i", path, "-an",
		"-vf", fmt.Sprintf("fps=1/%d,scale=%d:-2", videoFrameIntervalSec, videoFrameWidth),
		"-frames:v", strconv.Itoa(count),
		"-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
	}
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decode video: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	frames := splitJPEGStream(raw)
	out := make([]videoFrame, 0, len(frames))
	for i, frame := range frames {
		out = append(out, videoFrame{
			data: frame,
			at:   float64(i * videoFrameIntervalSec),
		})
	}
	return out, nil
}

func splitJPEGStream(raw []byte) [][]byte {
	var frames [][]byte
	start := -1
	for i := 0; i+1 < len(raw); i++ {
		if raw[i] == 0xFF && raw[i+1] == 0xD8 {
			start = i
			continue
		}
		if raw[i] == 0xFF && raw[i+1] == 0xD9 && start >= 0 {
			frame := raw[start : i+2]
			if _, err := jpeg.DecodeConfig(bytes.NewReader(frame)); err == nil {
				frames = append(frames, append([]byte(nil), frame...))
			}
			start = -1
			i++
		}
	}
	return frames
}
