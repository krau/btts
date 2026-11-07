package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrMediaTooLarge marks media that exceeds the configured download limit.
// It is a permanent skip, not a retryable enrichment failure.
var ErrMediaTooLarge = errors.New("media exceeds the configured size limit")

// SleepContext waits for d or until ctx is cancelled, whichever comes first.
func SleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// CappedBuffer is a bytes.Buffer that rejects writes beyond Limit.
type CappedBuffer struct {
	bytes.Buffer
	Limit int64
}

func (b *CappedBuffer) Write(p []byte) (int, error) {
	if b.Limit > 0 && int64(b.Len()+len(p)) > b.Limit {
		return 0, fmt.Errorf("%w: data exceeds %d bytes", ErrMediaTooLarge, b.Limit)
	}
	return b.Buffer.Write(p)
}
