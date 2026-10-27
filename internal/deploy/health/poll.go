package health

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Poll waits until both health endpoints return 200.
func Poll(ctx context.Context, baseURL string, timeout time.Duration) error {
	if ctx == nil {
		return fmt.Errorf("health: context required")
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	client := &http.Client{Timeout: 5 * time.Second}
	paths := []string{"/health", "/health/backends"}
	for {
		ok := true
		for _, p := range paths {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+p, nil)
			if err != nil {
				return err
			}
			resp, err := outbound.Do(ctx, outbound.DepHealth, client, req)
			if err != nil {
				ok = false
				break
			}
			if resp.StatusCode != http.StatusOK {
				discardAndClose(resp.Body)
				ok = false
				break
			}
			discardAndClose(resp.Body)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health: timeout waiting for %s", baseURL)
		}
		if err := sleepCtx(ctx, 2*time.Second); err != nil {
			return err
		}
	}
}

func discardAndClose(rc io.ReadCloser) {
	if rc == nil {
		return
	}
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
