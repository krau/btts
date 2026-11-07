package utils

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

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
		return 0, fmt.Errorf("data exceeds %d bytes", b.Limit)
	}
	return b.Buffer.Write(p)
}
