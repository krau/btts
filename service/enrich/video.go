package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const (
	videoFrameIntervalSec = 10
	videoFrameWidth       = 512
	maxVideoFrames        = 8
	videoKeyframeSnapSec  = 2.0
)

type videoFrame struct {
	data []byte
	at   float64
}

// videoFrames extracts up to count JPEG frames evenly across the video,
// preferring nearby keyframes. Frames are scaled to at most 512 pixels wide
// before embedding.
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

	duration, keyframes, err := probeVideo(ctx, path)
	if err != nil || duration <= 0 {
		return videoFramesAtInterval(ctx, ffmpeg, path, count)
	}

	frames := make([]videoFrame, 0, count)
	for _, at := range chooseFrameTimes(duration, count, keyframes) {
		frame, err := videoFrameAt(ctx, ffmpeg, path, at)
		if err != nil {
			return nil, err
		}
		frames = append(frames, videoFrame{data: frame, at: at})
	}
	return frames, nil
}

type videoProbeResult struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Packets []struct {
		PTS   string `json:"pts_time"`
		Flags string `json:"flags"`
	} `json:"packets"`
}

// probeVideo reads duration and keyframe timestamps from packet metadata. It
// demuxes the container but does not decode video frames, so it is much cheaper
// than scanning the full video for scene changes.
func probeVideo(ctx context.Context, path string) (float64, []float64, error) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0, nil, err
	}
	out, err := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "format=duration:packet=pts_time,flags",
		"-of", "json",
		path,
	).Output()
	if err != nil {
		return 0, nil, err
	}
	var result videoProbeResult
	if err := json.Unmarshal(out, &result); err != nil {
		return 0, nil, err
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(result.Format.Duration), 64)
	if err != nil {
		return 0, nil, err
	}
	keyframes := make([]float64, 0, len(result.Packets)/10)
	for _, packet := range result.Packets {
		if !strings.Contains(packet.Flags, "K") {
			continue
		}
		at, err := strconv.ParseFloat(packet.PTS, 64)
		if err != nil || at < 0 || at >= duration {
			continue
		}
		keyframes = append(keyframes, at)
	}
	sort.Float64s(keyframes)
	return duration, keyframes, nil
}

func chooseFrameTimes(duration float64, count int, keyframes []float64) []float64 {
	uniform := make([]float64, count)
	for i := range count {
		// Segment centers avoid intros and trailing fades.
		uniform[i] = duration * (float64(i) + 0.5) / float64(count)
	}
	times := make([]float64, 0, count)
	for _, target := range uniform {
		at := target
		if len(keyframes) > 0 {
			idx := sort.SearchFloat64s(keyframes, target)
			best := -1.0
			bestDist := videoKeyframeSnapSec
			if idx < len(keyframes) {
				best, bestDist = keyframes[idx], keyframes[idx]-target
			}
			if idx > 0 {
				dist := target - keyframes[idx-1]
				if dist < bestDist {
					best, bestDist = keyframes[idx-1], dist
				}
			}
			if best >= 0 && bestDist <= videoKeyframeSnapSec {
				at = best
			}
		}
		if !containsTime(times, at, 0.25) {
			times = append(times, at)
		}
	}
	for _, target := range uniform {
		if len(times) >= count {
			break
		}
		if !containsTime(times, target, 0.25) {
			times = append(times, target)
		}
	}
	// Keyframe snapping can collapse nearby targets; fill any remaining slots.
	for _, target := range uniform {
		if len(times) >= count {
			break
		}
		times = append(times, target)
	}
	sort.Float64s(times)
	return times
}

func containsTime(times []float64, at, tolerance float64) bool {
	for _, existing := range times {
		if diff := existing - at; diff >= -tolerance && diff <= tolerance {
			return true
		}
	}
	return false
}

func videoFrameAt(ctx context.Context, ffmpeg, path string, at float64) ([]byte, error) {
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-ss", strconv.FormatFloat(at, 'f', 3, 64),
		"-i", path, "-an",
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-2", videoFrameWidth),
		// Some phone videos carry non-standard YUV ranges; MJPEG rejects
		// them unless ffmpeg's strictness is relaxed.
		"-strict", "unofficial",
		"-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
	}
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decode video at %.3fs: %w: %s", at, err, strings.TrimSpace(stderr.String()))
	}
	frames := splitJPEGStream(raw)
	if len(frames) == 0 {
		return nil, fmt.Errorf("ffmpeg returned no frame at %.3fs", at)
	}
	return frames[0], nil
}

// videoFramesAtInterval is the fallback when ffprobe cannot determine the
// duration. It samples from the beginning at a fixed interval.
func videoFramesAtInterval(ctx context.Context, ffmpeg, path string, count int) ([]videoFrame, error) {
	args := []string{
		"-hide_banner", "-loglevel", "error", "-i", path, "-an",
		"-vf", fmt.Sprintf("fps=1/%d,scale=%d:-2", videoFrameIntervalSec, videoFrameWidth),
		"-frames:v", strconv.Itoa(count),
		"-strict", "unofficial",
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
