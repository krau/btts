package enrich

import (
	"context"
	"math"
	"testing"
)

func TestAudioWindowsWithFFmpeg(t *testing.T) {
	if _, err := findFFmpeg(); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	const seconds = 130
	samples := make([]int16, audioSampleRate*seconds)
	for i := range samples {
		samples[i] = int16(10000 * math.Sin(2*math.Pi*440*float64(i)/audioSampleRate))
	}
	windows, err := audioWindows(context.Background(), wavBytes(samples), false)
	if err != nil {
		t.Fatalf("audio windows: %v", err)
	}
	if len(windows) != maxAudioWindows {
		t.Fatalf("got %d windows, want %d", len(windows), maxAudioWindows)
	}
	for i, window := range windows {
		wantStart := float64(i * audioHopSec)
		if math.Abs(window.start-wantStart) > 0.01 {
			t.Fatalf("window %d start = %f, want %f", i, window.start, wantStart)
		}
		if got := window.end - window.start; math.Abs(got-audioWindowSec) > 0.01 {
			t.Fatalf("window %d length = %f, want %d", i, got, audioWindowSec)
		}
	}

	silence := make([]int16, audioSampleRate*60)
	windows, err = audioWindows(context.Background(), wavBytes(silence), false)
	if err != nil {
		t.Fatalf("silent audio windows: %v", err)
	}
	if len(windows) != 0 {
		t.Fatalf("silent audio produced %d windows", len(windows))
	}
}
