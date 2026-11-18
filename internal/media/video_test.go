package media

import (
	"bytes"
	"context"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVideoFramesWithFFmpeg(t *testing.T) {
	ffmpeg, err := FFmpegPath()
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.CommandContext(t.Context(), ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=240x320:d=25",
		"-an", "-c:v", "mpeg4", "-pix_fmt", "yuv420p", "-y", path,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate sample video: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := VideoFrames(context.Background(), data, 3)
	if err != nil {
		t.Fatalf("extract frames: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}
	for i, frame := range frames {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(frame.JPEG))
		if err != nil {
			t.Fatalf("frame %d is not a JPEG: %v", i, err)
		}
		if cfg.Width != 384 || cfg.Height != 512 {
			t.Fatalf("frame %d dimensions = %dx%d, want 384x512", i, cfg.Width, cfg.Height)
		}
		wantAt := 25 * (float64(i) + 0.5) / 3
		if math.Abs(frame.At-wantAt) > videoKeyframeSnapSec {
			t.Fatalf("frame %d timestamp = %v, want near %v", i, frame.At, wantAt)
		}
		if i > 0 && frame.At <= frames[i-1].At {
			t.Fatalf("frame timestamps are not increasing: %v then %v", frames[i-1].At, frame.At)
		}
	}
}
