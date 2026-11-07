package enrich

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"unicode/utf8"
)

type pdfExtractor struct {
	binary   string
	maxChars int
	once     sync.Once
	missing  bool
}

func newPDFExtractor(binary string, maxChars int) *pdfExtractor {
	if binary == "" {
		binary = "pdftotext"
	}
	if maxChars <= 0 {
		maxChars = 12000
	}
	return &pdfExtractor{binary: binary, maxChars: maxChars}
}

func (p *pdfExtractor) available() bool {
	p.once.Do(func() {
		_, err := exec.LookPath(p.binary)
		p.missing = err != nil
	})
	return !p.missing
}

// Extract returns the text layer of a PDF, truncated to maxChars.
func (p *pdfExtractor) Extract(ctx context.Context, data []byte) (string, error) {
	if !p.available() {
		return "", fmt.Errorf("%s not found", p.binary)
	}
	cmd := exec.CommandContext(ctx, p.binary, "-layout", "-enc", "UTF-8", "-", "-")
	cmd.Stdin = bytes.NewReader(data)
	var out limitedBuffer
	out.limit = p.maxChars * 8
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if text == "" && err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	if len(text) > p.maxChars {
		text = text[:p.maxChars]
		for !utf8.ValidString(text) && len(text) > 0 {
			text = text[:len(text)-1]
		}
	}
	return strings.ReplaceAll(text, "\x00", ""), nil
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }
