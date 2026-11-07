package enrich

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVideoFramesWithFFmpeg(t *testing.T) {
	ffmpeg, err := findFFmpeg()
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=320x240:d=25",
		"-an", "-c:v", "mpeg4", "-pix_fmt", "yuv420p", "-y", path,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate sample video: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := videoFrames(context.Background(), data, 3)
	if err != nil {
		t.Fatalf("extract frames: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}
	for i, frame := range frames {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(frame.data))
		if err != nil {
			t.Fatalf("frame %d is not a JPEG: %v", i, err)
		}
		if cfg.Width != videoFrameWidth {
			t.Fatalf("frame %d width = %d, want %d", i, cfg.Width, videoFrameWidth)
		}
		if frame.at != float64(i*videoFrameIntervalSec) {
			t.Fatalf("frame %d timestamp = %v", i, frame.at)
		}
	}
}
