package agent

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"google.golang.org/genai"

	"github.com/krau/btts/internal/media"
)

const (
	videoPreviewFrames  = 2
	videoPreviewTimeout = 5 * time.Second
)

type mediaInputs uint8

const (
	imageInput mediaInputs = 1 << iota
	audioInput
	videoInput
)

func resolveMediaInputs(provider string, configured []string) (mediaInputs, error) {
	// The ADK OpenAI adapter cannot serialize audio or video on either API,
	// regardless of what the model behind an OpenAI-compatible endpoint accepts.
	transport := imageInput
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gemini", "google":
		transport |= audioInput | videoInput
	}
	if len(configured) == 0 {
		return transport, nil
	}
	var enabled mediaInputs
	for _, input := range configured {
		switch strings.ToLower(strings.TrimSpace(input)) {
		case "text":
		case "image":
			enabled |= imageInput
		case "audio":
			enabled |= audioInput
		case "video":
			enabled |= videoInput
		default:
			return 0, fmt.Errorf("invalid agent.input_modalities value %q (use text, image, audio, or video)", input)
		}
	}
	return enabled & transport, nil
}

func inlineMIME(data []byte, reported string) string {
	mimeType, _, _ := mime.ParseMediaType(reported)
	detected := http.DetectContentType(data)
	// The loader can guess an image's MIME type from an incorrect filename.
	if strings.HasPrefix(detected, "image/") || strings.HasPrefix(mimeType, "image/") ||
		mimeType == "" || mimeType == "application/octet-stream" {
		return detected
	}
	return mimeType
}

func (inputs mediaInputs) prepare(ctx context.Context, data []byte, reportedMIME string) ([]*genai.Part, string) {
	mimeType := inlineMIME(data, reportedMIME)
	var native mediaInputs
	switch {
	case mimeType == "image/jpeg" || mimeType == "image/png" || mimeType == "image/webp":
		native = imageInput
	case strings.HasPrefix(mimeType, "audio/"):
		native = audioInput
	case strings.HasPrefix(mimeType, "video/"):
		native = videoInput
	}
	if inputs&native != 0 {
		return []*genai.Part{genai.NewPartFromBytes(data, mimeType)}, ""
	}
	if inputs&imageInput != 0 && (native == videoInput || mimeType == "image/gif") {
		previewCtx, cancel := context.WithTimeout(ctx, videoPreviewTimeout)
		defer cancel()
		frames, err := media.VideoFrames(previewCtx, data, videoPreviewFrames)
		if err != nil {
			log.FromContext(ctx).Debug("Extract video preview", "error", err)
		}
		if len(frames) == 0 {
			return nil, "A video preview could not be generated. The original media is unavailable to the model."
		}
		parts := make([]*genai.Part, 0, len(frames))
		for _, frame := range frames {
			parts = append(parts, genai.NewPartFromBytes(frame.JPEG, "image/jpeg"))
		}
		return parts, "Only sampled still frames are available, without motion or audio."
	}
	return nil, "This media format is not supported by the current model."
}
