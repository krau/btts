package config

import (
	"os"
	"testing"
	"time"
)

func TestInitDecodesDurationsAndNormalizes(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := `
app_id = 1
app_hash = "x"
bot_token = "x"
[import]
batch_delay = "2s"
background = false
[enrich]
enable = true
retry_backoff = "45s"
flood_wait_extra = "6s"
max_attempts = 0
[enrich.embedder]
enable = true
url = "http://127.0.0.1:11434"
model = "m"
dimensions = 8
timeout = "60s"
query_timeout = "7s"
[enrich.tagger]
timeout = "8s"
`
	if err := os.WriteFile("config", []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	Init()
	if C.Import.BatchDelay != 2*time.Second || C.Import.Background {
		t.Fatalf("import config not decoded: %+v", C.Import)
	}
	if C.Enrich.RetryBackoff != 45*time.Second || C.Enrich.FloodWaitExtra != 6*time.Second {
		t.Fatalf("enrich durations not decoded: %+v", C.Enrich)
	}
	if C.Enrich.Embedder.Timeout != 60*time.Second || C.Enrich.Embedder.QueryTimeout != 7*time.Second {
		t.Fatalf("embedder durations not decoded: %+v", C.Enrich.Embedder)
	}
	if C.Enrich.Tagger.Timeout != 8*time.Second {
		t.Fatalf("tagger duration not decoded: %+v", C.Enrich.Tagger)
	}
	if C.Enrich.MaxAttempts != 5 {
		t.Fatalf("max attempts not normalized: %d", C.Enrich.MaxAttempts)
	}
}

func TestNormalizeDefaults(t *testing.T) {
	old := C
	t.Cleanup(func() { C = old })
	C = AppConfig{}
	normalize()
	if C.Enrich.Workers != 1 ||
		C.Enrich.MaxDownloadMB != 20 ||
		C.Enrich.MaxAttempts != 5 ||
		C.Enrich.RetryBackoff != 30*time.Second ||
		C.Enrich.MediaFetchConcurrency != 1 ||
		C.Enrich.MediaFetchRetries != 3 ||
		C.Enrich.Embedder.Timeout != 30*time.Second ||
		C.Enrich.Embedder.QueryTimeout != 10*time.Second ||
		C.Enrich.Tagger.Timeout != 5*time.Second ||
		C.Enrich.Documents.PDFToText != "pdftotext" ||
		C.Enrich.Documents.MaxSizeMB != 20 ||
		C.Enrich.Documents.MaxChars != 12000 {
		t.Fatalf("unexpected defaults: %+v", C.Enrich)
	}
}
