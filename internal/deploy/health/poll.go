// Package health waits for a deployed gateway to answer its health endpoints, and
// dumps the gateway's logs and state when a deploy fails.
package health

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// pollInterval is the pause between rounds of health checks.
const pollInterval = 2 * time.Second

// endpoints must all answer 200 for the gateway to count as up.
var endpoints = []string{"/health", "/health/backends"}

// Poll waits until both health endpoints at baseURL answer 200. ctx must carry a
// deadline: it is the health budget. On timeout the error wraps the last failure
// (transport error or status), so a TLS, DNS or 401 problem is not reported as a
// gateway that is merely down. Each GET is sent once; the loop is the retry.
func Poll(ctx context.Context, client *http.Client, baseURL string) error {
	const op = "health.Poll"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return derrors.New(derrors.CodeFailedPrecondition, op, "context needs a deadline")
	}
	if client == nil {
		return derrors.New(derrors.CodeInvalid, op, "http client required")
	}
	var lastErr error
	for {
		lastErr = checkOnce(ctx, client, baseURL)
		if lastErr == nil {
			return nil
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			code := derrors.CodeTimeout
			if errors.Is(err, context.Canceled) {
				code = derrors.CodeCanceled
			}
			return derrors.Wrap(lastErr, code, op, "timeout waiting for "+baseURL)
		}
	}
}

// checkOnce GETs each endpoint once and returns the first failure.
func checkOnce(ctx context.Context, client *http.Client, baseURL string) error {
	for _, p := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+p, nil)
		if err != nil {
			return fmt.Errorf("GET %s: %w", p, err)
		}
		resp, err := outbound.DoOnce(ctx, outbound.DepHealth, client, req)
		if err != nil {
			return fmt.Errorf("GET %s: %w", p, err)
		}
		status := resp.StatusCode
		discardAndClose(resp.Body)
		if status != http.StatusOK {
			return fmt.Errorf("GET %s: status %d", p, status)
		}
	}
	return nil
}

func discardAndClose(rc io.ReadCloser) {
	if rc == nil {
		return
	}
	// Cleanup only: a drain error costs connection reuse, nothing else.
	defer func() {
		_, _ = io.Copy(io.Discard, rc)
		_ = rc.Close()
	}()
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
