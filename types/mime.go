package types

import (
	"path"
	"strings"
)

// MIMEFromFilename maps a file name to the MIME type understood by enrichment.
func MIMEFromFilename(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".m4a":
		return "audio/mp4"
	case ".mp4", ".mov", ".mkv", ".webm":
		return "video/mp4"
	}
	return ""
}

// MIMEForMessageType returns the MIME type implied by a Telegram message type.
func MIMEForMessageType(messageType MessageType) string {
	switch messageType {
	case MessageTypePhoto:
		return "image/jpeg"
	case MessageTypeVoice:
		return "audio/ogg"
	case MessageTypeAudio:
		return "audio/mpeg"
	case MessageTypeVideo:
		return "video/mp4"
	default:
		return ""
	}
}

// MessageMIME resolves a MIME type from the document, its file name, or the
// Telegram message type. Voice/audio/video documents often carry no MIME.
func MessageMIME(mime, name string, messageType MessageType) string {
	mime = EffectiveMIME(mime, name)
	if mime == "" || mime == "application/octet-stream" {
		return MIMEForMessageType(messageType)
	}
	return mime
}

// EffectiveMIME fills in a missing or generic MIME type from the file name.
func EffectiveMIME(mime, name string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if mime == "" || mime == "application/octet-stream" {
		if inferred := MIMEFromFilename(name); inferred != "" {
			return inferred
		}
	}
	return mime
}

// IsPDF reports whether the media is a PDF document.
func IsPDF(mime, name string) bool {
	return EffectiveMIME(mime, name) == "application/pdf" ||
		strings.HasSuffix(strings.ToLower(name), ".pdf")
}

// IsSupportedImage reports whether both the embedder and the tagger can consume
// the image format.
func IsSupportedImage(mime, name string) bool {
	switch EffectiveMIME(mime, name) {
	case "image/jpeg", "image/jpg", "image/png":
		return true
	}
	return false
}

// IsSupportedAV reports whether the media is an audio or video stream that
// ffmpeg can decode.
func IsSupportedAV(mime, name string) bool {
	mime = EffectiveMIME(mime, name)
	return strings.HasPrefix(mime, "audio/") || strings.HasPrefix(mime, "video/")
}
