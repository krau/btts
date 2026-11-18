package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model/openaimodel"

	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/internal/media"
	"github.com/krau/btts/types"
)

func TestResolveMediaInputs(t *testing.T) {
	for _, tc := range []struct {
		provider   string
		modalities []string
		want       mediaInputs
	}{
		{"openai", nil, imageInput},
		{"openai-compatible", []string{"image", "audio", "video"}, imageInput},
		{"ollama", nil, imageInput},
		{"gemini", nil, imageInput | audioInput | videoInput},
		{"google", []string{"text", " Image "}, imageInput},
		{"gemini", []string{"text"}, 0},
	} {
		got, err := resolveMediaInputs(tc.provider, tc.modalities)
		if err != nil || got != tc.want {
			t.Errorf("resolveMediaInputs(%q, %v) = %v, %v; want %v", tc.provider, tc.modalities, got, err, tc.want)
		}
	}
	if _, err := resolveMediaInputs("openai", []string{"images"}); err == nil {
		t.Error("expected an error for an unknown modality")
	}
}

func TestNativeVideoInput(t *testing.T) {
	inputs, err := resolveMediaInputs("gemini", nil)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("original video data")
	parts, note := inputs.prepare(t.Context(), data, " Video/MP4; codecs=h264")
	if len(parts) != 1 || note != "" || parts[0].InlineData.MIMEType != "video/mp4" ||
		!bytes.Equal(parts[0].InlineData.Data, data) {
		t.Fatalf("expected original video, got %v (%s)", parts, note)
	}
}

func TestOpenAIAgentHandlesVideoAndAudio(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := database.InitDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	docs := []*types.MessageDocument{
		{ID: 1, ChatID: 42, Type: int(types.MessageTypeVideo), Message: "video caption", Ocred: "video OCR", Tags: []string{"tag"}},
		{ID: 2, ChatID: 42, Type: int(types.MessageTypeVoice), Message: "voice caption"},
	}
	_, ffmpegErr := media.FFmpegPath()
	for _, tc := range []struct {
		name     string
		textOnly bool
		preview  bool
	}{
		{name: "preview", preview: true},
		{name: "invalid video"},
		{name: "text only", textOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var configured []string
			if tc.textOnly {
				configured = []string{"text"}
			}
			inputs, err := resolveMediaInputs("openai", configured)
			if err != nil {
				t.Fatal(err)
			}
			loader := &testMediaLoader{video: []byte("invalid video")}
			if tc.preview {
				loader.video = sampleVideo(t)
			}
			calls := 0
			wantFrames := 0
			transport := testTransport(func(r *http.Request) (*http.Response, error) {
				defer r.Body.Close()
				calls++
				var req struct {
					Messages []struct {
						Content    json.RawMessage `json:"content"`
						ToolCallID string          `json:"tool_call_id"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					return nil, err
				}
				reply := `{"finish_reason":"stop","message":{"role":"assistant","content":"search answer"}}`
				switch calls {
				case 1:
					if got := bytes.Contains(req.Messages[len(req.Messages)-1].Content, []byte("data:image/png;base64,")); got == tc.textOnly {
						t.Errorf("user image present = %v, want %v", got, !tc.textOnly)
					}
					reply = toolCallChoice("get", "get_messages", `{"chat_id":42,"message_ids":[1,2],"include_media":true}`)
				case 2:
					last := req.Messages[len(req.Messages)-1]
					if last.ToolCallID != "get" {
						t.Errorf("expected get_messages response, got %s", last.Content)
					}
					hits, err := readTestToolHits(last.Content)
					if err != nil {
						t.Errorf("invalid tool response: %v", err)
						return nil, err
					}
					if len(hits) != 2 {
						return nil, fmt.Errorf("expected two hits, got %v", hits)
					}
					video, voice := hits[0], hits[1]
					if video.Message != "video caption" || video.OCRed != "video OCR" || len(video.Tags) != 1 || video.Link != "https://t.me/c/42/1" {
						t.Errorf("video metadata was not preserved: %+v", video)
					}
					if len(voice.MediaArtifacts) != 0 || voice.MediaNote == "" {
						t.Errorf("expected voice to be metadata-only: %+v", voice)
					}
					if video.MediaNote == "" || (len(video.MediaArtifacts) > 0) != tc.preview {
						t.Errorf("unexpected video media result: %+v", video)
					}
					if tc.preview {
						wantFrames = len(video.MediaArtifacts)
						if wantFrames > videoPreviewFrames {
							t.Errorf("returned %d video artifacts, limit %d", wantFrames, videoPreviewFrames)
						}
						args, err := json.Marshal(map[string][]string{"artifact_names": video.MediaArtifacts})
						if err != nil {
							return nil, err
						}
						reply = toolCallChoice("load", "load_artifacts", string(args))
					}
				case 3:
					frames := 0
					for _, msg := range req.Messages {
						frames += bytes.Count(msg.Content, []byte("data:image/jpeg;base64,"))
					}
					if frames != wantFrames {
						t.Errorf("sent %d JPEG frames, want %d", frames, wantFrames)
					}
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"completion","choices":[` + reply + `]}`)),
				}, nil
			})
			llm, err := openaimodel.NewModel(t.Context(), "test-model", &openaimodel.ClientConfig{
				API:        openaimodel.APIChatCompletions,
				APIKey:     "test-key",
				BaseURL:    "https://example.invalid/v1",
				HTTPClient: &http.Client{Transport: transport},
			})
			if err != nil {
				t.Fatal(err)
			}
			var cfg config.AppConfig
			cfg.Agent.MaxLLMCalls = 4
			cfg.Agent.Timeout = 10 * time.Second
			svc := &Service{
				model: llm, searcher: testMediaSearcher{docs: docs}, cfg: cfg,
				mediaInputs: inputs, mediaLoader: loader, sem: make(chan struct{}, 1),
			}
			answer, err := svc.AnswerWithAttachments(t.Context(), "Describe the media.", Scope{AllowedChatIDs: []int64{42}}, []Attachment{
				{Data: photo.Bytes(), MIME: "image/jpeg"},
				{Data: []byte("wave data"), MIME: "audio/wav"},
			})
			if err != nil || answer != "search answer" {
				t.Fatalf("agent answer = %q, %v", answer, err)
			}
			wantCalls := 2
			if tc.preview {
				wantCalls = 3
			}
			if calls != wantCalls {
				t.Errorf("made %d model calls, want %d", calls, wantCalls)
			}
			if want := !tc.textOnly && ffmpegErr == nil; loader.loadedVideo != want {
				t.Errorf("downloaded video = %v, want %v", loader.loadedVideo, want)
			}
		})
	}
}

type testMediaSearcher struct {
	engine.Searcher
	docs []*types.MessageDocument
}

func (s testMediaSearcher) GetDocuments(context.Context, int64, []int) ([]*types.MessageDocument, error) {
	return s.docs, nil
}

type testMediaLoader struct {
	video       []byte
	loadedVideo bool
}

func (l *testMediaLoader) LoadMedia(_ context.Context, _ int64, id int) ([]byte, string, string, error) {
	if id != 1 {
		panic("unsupported audio must not be downloaded")
	}
	l.loadedVideo = true
	return l.video, "video/mp4", "sample.mp4", nil
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func toolCallChoice(id, name, args string) string {
	return fmt.Sprintf(`{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"type":"function","id":%q,"function":{"name":%q,"arguments":%q}}]}}`, id, name, args)
}

func readTestToolHits(content json.RawMessage) ([]toolHit, error) {
	var text string
	if err := json.Unmarshal(content, &text); err != nil {
		return nil, err
	}
	var result getMessagesOutput
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, err
	}
	return result.Messages, nil
}

func sampleVideo(t *testing.T) []byte {
	t.Helper()
	ffmpeg, err := media.FFmpegPath()
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=32x32:d=3", "-an", "-c:v", "mpeg4", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate sample video: %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
