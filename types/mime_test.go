package types

import "testing"

func TestMIMEHelpers(t *testing.T) {
	cases := []struct {
		name    string
		mime    string
		file    string
		msgType MessageType
		want    string
	}{
		{"extension fallback", "application/octet-stream", "clip.MP4", MessageTypeDocument, "video/mp4"},
		{"generic mime fallback", "", "song.flac", MessageTypeDocument, "audio/flac"},
		{"known mime", "image/png", "", MessageTypeDocument, "image/png"},
		{"voice without mime", "", "", MessageTypeVoice, "audio/ogg"},
		{"unknown", "", "", MessageTypeText, ""},
	}
	for _, tc := range cases {
		if got := MessageMIME(tc.mime, tc.file, tc.msgType); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if !IsPDF("application/octet-stream", "book.PDF") {
		t.Error("PDF by extension not detected")
	}
	if IsSupportedImage("image/webp", "x.webp") {
		t.Error("webp must not be treated as a supported image")
	}
	if !IsSupportedImage("image/jpeg", "") {
		t.Error("jpeg must be a supported image")
	}
	if !IsSupportedAV("", "voice.opus") {
		t.Error("opus must be supported audio")
	}
}

func TestDocumentFullText(t *testing.T) {
	doc := MessageDocument{
		Message:   "message",
		Ocred:     "ocr",
		Tags:      []string{"cat", "sunset"},
		Extracted: "pdf text",
	}
	want := "message ocr pdf text cat sunset"
	if got := doc.FullText(); got != want {
		t.Fatalf("FullText: got %q, want %q", got, want)
	}
	doc.TagsText = "cat sunset"
	if got := doc.FullText(); got != want {
		t.Fatalf("FullText with TagsText: got %q, want %q", got, want)
	}
}
