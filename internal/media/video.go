package media

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
	videoFrameScale       = "scale=512:512:force_original_aspect_ratio=decrease:force_divisible_by=2"
	maxVideoFrames        = 8
	videoKeyframeSnapSec  = 2.0
)

type VideoFrame struct {
	JPEG []byte
	At   float64 // seconds from the start of the video
}

// VideoFrames extracts up to count JPEG frames across the video, preferring
// nearby keyframes. It may return usable frames alongside a decoding error.
func VideoFrames(ctx context.Context, data []byte, count int) ([]VideoFrame, error) {
	ffmpeg, err := FFmpegPath()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is required to extract video frames: %w", err)
	}
	if count <= 0 {
		count = 3
	}
	if count > maxVideoFrames {
		count = maxVideoFrames
	}
	path, cleanup, err := WriteTempFile(data)
	if err != nil {
		return nil, fmt.Errorf("write video temp file: %w", err)
	}
	defer cleanup()

	duration, keyframes, err := probeVideo(ctx, path)
	if err != nil || duration <= 0 {
		return videoFramesAtInterval(ctx, ffmpeg, path, count)
	}

	frames := make([]VideoFrame, 0, count)
	for _, at := range chooseFrameTimes(duration, count, keyframes) {
		frame, err := videoFrameAt(ctx, ffmpeg, path, at)
		if err != nil {
			return frames, err
		}
		frames = append(frames, VideoFrame{JPEG: frame, At: at})
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
	args := append([]string{"-v", "error"}, videoInputOptions()...)
	args = append(args,
		"-select_streams", "v:0",
		"-show_entries", "format=duration:packet=pts_time,flags",
		"-of", "json",
		path,
	)
	out, err := exec.CommandContext(ctx, ffprobe, args...).Output()
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
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, videoInputOptions()...)
	args = append(args,
		"-ss", strconv.FormatFloat(at, 'f', 3, 64),
		"-i", path, "-an",
		"-frames:v", "1",
		"-vf", videoFrameScale,
		// Some phone videos carry non-standard YUV ranges; MJPEG rejects
		// them unless ffmpeg's strictness is relaxed.
		"-strict", "unofficial",
		"-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
	)
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
func videoFramesAtInterval(ctx context.Context, ffmpeg, path string, count int) ([]VideoFrame, error) {
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, videoInputOptions()...)
	args = append(args,
		"-i", path, "-an",
		"-vf", fmt.Sprintf("fps=1/%d,%s", videoFrameIntervalSec, videoFrameScale),
		"-frames:v", strconv.Itoa(count),
		"-strict", "unofficial",
		"-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
	)
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decode video: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	frames := splitJPEGStream(raw)
	if len(frames) == 0 {
		frame, err := videoFrameAt(ctx, ffmpeg, path, 0)
		if err != nil {
			return nil, err
		}
		return []VideoFrame{{JPEG: frame}}, nil
	}
	out := make([]VideoFrame, 0, len(frames))
	for i, frame := range frames {
		out = append(out, VideoFrame{
			JPEG: frame,
			At:   float64(i * videoFrameIntervalSec),
		})
	}
	return out, nil
}

func videoInputOptions() []string {
	// Downloaded files must not be interpreted as playlists or scripts.
	return []string{
		"-protocol_whitelist", "file",
		"-format_whitelist", "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,gif,avi,asf,flv,mpeg,mpegts,mpegtsraw,mpegvideo,ogg,rm,h264,hevc,mjpeg",
	}
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
