package enrich

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"sync"
)

const (
	audioSampleRate = 16000
	audioWindowSec  = 30
	audioHopSec     = 25
	maxAudioWindows = 4
	// Decode only the part of the stream that can produce a window.
	audioDecodeLimitSec = audioWindowSec + maxAudioWindows*audioHopSec
)

type audioWindow struct {
	wav   []byte
	start float64
	end   float64
}

var (
	ffmpegOnce   sync.Once
	ffmpegPath   string
	ffmpegLookup error
)

func findFFmpeg() (string, error) {
	ffmpegOnce.Do(func() {
		ffmpegPath, ffmpegLookup = exec.LookPath("ffmpeg")
	})
	return ffmpegPath, ffmpegLookup
}

// audioWindows decodes the audio track (or a video's soundtrack) to 16 kHz
// mono and cuts it into 30-second windows with a 25-second hop. Ollama's model
// ignores audio beyond 30 seconds in a single input.
func audioWindows(ctx context.Context, data []byte, video bool) ([]audioWindow, error) {
	ffmpeg, err := findFFmpeg()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is required for audio enrichment: %w", err)
	}
	input := "pipe:0"
	if video {
		// MP4 indexes often live at the end of the file, so ffmpeg needs a
		// seekable input rather than a pipe.
		path, cleanup, err := writeTempMedia(data)
		if err != nil {
			return nil, fmt.Errorf("write video temp file: %w", err)
		}
		defer cleanup()
		input = path
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-i", input}
	if video {
		args = append(args, "-vn")
	}
	args = append(args, "-t", fmt.Sprintf("%d", audioDecodeLimitSec), "-ac", "1", "-ar", fmt.Sprintf("%d", audioSampleRate), "-f", "s16le", "pipe:1")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if !video {
		cmd.Stdin = bytes.NewReader(data)
	}
	raw, err := cmd.Output()
	if err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if strings.Contains(stderrText, "does not contain any stream") ||
			strings.Contains(stderrText, "matches no streams") {
			return nil, nil
		}
		return nil, fmt.Errorf("ffmpeg decode: %w: %s", err, stderrText)
	}
	if len(raw) < 2 {
		return nil, nil
	}
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	window := audioWindowSec * audioSampleRate
	hop := audioHopSec * audioSampleRate
	var windows []audioWindow
	offset := 0
	for len(windows) < maxAudioWindows && offset+window <= len(samples) {
		if rms(samples[offset:offset+window]) > 0.003 {
			windows = append(windows, audioWindow{
				wav:   wavBytes(samples[offset : offset+window]),
				start: float64(offset) / audioSampleRate,
				end:   float64(offset+window) / audioSampleRate,
			})
		}
		offset += hop
	}
	if len(windows) < maxAudioWindows && offset < len(samples) {
		tail := samples[offset:]
		if len(tail) >= 2*audioSampleRate && rms(tail) > 0.003 {
			windows = append(windows, audioWindow{
				wav:   wavBytes(tail),
				start: float64(offset) / audioSampleRate,
				end:   float64(len(samples)) / audioSampleRate,
			})
		}
	}
	return windows, nil
}

func rms(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, sample := range samples {
		value := float64(sample) / 32768
		sum += value * value
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func wavBytes(samples []int16) []byte {
	rate := uint32(audioSampleRate)
	dataLen := uint32(len(samples) * 2)
	out := make([]byte, 44+len(samples)*2)
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], 36+dataLen)
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], 1)
	binary.LittleEndian.PutUint32(out[24:28], rate)
	binary.LittleEndian.PutUint32(out[28:32], rate*2)
	binary.LittleEndian.PutUint16(out[32:34], 2)
	binary.LittleEndian.PutUint16(out[34:36], 16)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], dataLen)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(out[44+i*2:], uint16(sample))
	}
	return out
}
