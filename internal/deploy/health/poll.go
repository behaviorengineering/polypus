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

// DefaultPollInterval is the pause between rounds of health checks when the
// caller passes zero.
const DefaultPollInterval = 2 * time.Second

// probeTimeout bounds one health GET, so a hung connection costs one round of the
// poll, not the whole health budget.
const probeTimeout = 5 * time.Second

// bodyDrainMax caps how much of a health body is drained for connection reuse.
const bodyDrainMax = 64 << 10

// endpoints must all answer 200 for the gateway to count as up.
var endpoints = []string{"/health", "/health/backends"}

// Poll waits until both health endpoints at baseURL answer 200. ctx must carry a
// deadline: it is the health budget. On timeout the error wraps the last failure
// (transport error or status), so a TLS, DNS or 401 problem is not reported as a
// gateway that is merely down. Each GET is sent once; the loop is the retry, with
// interval between rounds (zero or less uses DefaultPollInterval).
func Poll(ctx context.Context, client *http.Client, baseURL string, interval time.Duration) error {
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
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	var lastErr error
	for {
		lastErr = checkOnce(ctx, client, baseURL)
		if lastErr == nil {
			return nil
		}
		if err := sleepCtx(ctx, interval); err != nil {
			code := derrors.CodeTimeout
			if errors.Is(err, context.Canceled) {
				code = derrors.CodeCanceled
			}
			return derrors.Wrap(lastErr, code, op, "timeout waiting for "+baseURL)
		}
	}
}

// checkOnce GETs each endpoint once, each bounded by probeTimeout, and returns the
// first failure.
func checkOnce(ctx context.Context, client *http.Client, baseURL string) error {
	for _, p := range endpoints {
		status, err := probeStatus(ctx, client, baseURL+p)
		if err != nil {
			return fmt.Errorf("GET %s: %w", p, err)
		}
		if status != http.StatusOK {
			return fmt.Errorf("GET %s: status %d", p, status)
		}
	}
	return nil
}

// probeStatus sends one GET of url, bounded by probeTimeout, and returns its status.
func probeStatus(ctx context.Context, client *http.Client, url string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	return getStatus(ctx, client, req)
}

// getStatus sends req once and returns its status. The body is drained (up to
// bodyDrainMax) and closed by the deferred cleanup, which is where a discarded
// error belongs: a drain error costs connection reuse, nothing else.
func getStatus(ctx context.Context, client *http.Client, req *http.Request) (int, error) {
	resp, err := outbound.DoOnce(ctx, outbound.DepHealth, client, req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, bodyDrainMax))
		_ = resp.Body.Close()
	}()
	return resp.StatusCode, nil
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
