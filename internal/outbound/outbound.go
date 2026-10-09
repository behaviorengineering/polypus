package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// ErrAbort marks a non-retryable outbound failure.
var ErrAbort = errors.New("outbound: non-retryable")

// Dependency names for breaker keys.
const (
	DepGitHub       = "github"
	DepGHCR         = "ghcr"
	DepRegistry     = "registry"
	DepHealth       = "health"
	DepDockerDaemon = "docker-daemon"
	DepCloudflare   = "cloudflare"
	DepGateway      = "gateway"
)

// Abort wraps err so failsafe stops retrying.
func Abort(err error) error {
	if err == nil {
		return fmt.Errorf("%w: nil", ErrAbort)
	}
	return fmt.Errorf("%w: %v", ErrAbort, err)
}

// HTTPStatusRetryable reports whether an HTTP status should be retried.
func HTTPStatusRetryable(status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500 && status <= 599
}

// HTTPStatusPermanent reports whether an HTTP status must not be retried.
func HTTPStatusPermanent(status int) bool {
	if status == http.StatusTooManyRequests {
		return false
	}
	if status >= 200 && status < 300 {
		return false
	}
	if status >= 500 {
		return false
	}
	return status >= 400 && status < 500
}

// TransportRetryable reports whether a transport-level error should be retried.
func TransportRetryable(err error) bool {
	if err == nil || errors.Is(err, ErrAbort) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "temporary failure") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "broken pipe")
}

func validateCtx(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("outbound: context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("outbound: missing deadline")
	}
	return nil
}

var (
	breakerMu sync.Mutex
	breakers  = map[string]failsafe.Policy[any]{}
)

func breakerFor(name string) failsafe.Policy[any] {
	switch name {
	case DepGitHub, DepGHCR, DepRegistry, DepCloudflare:
	default:
		return nil
	}
	breakerMu.Lock()
	defer breakerMu.Unlock()
	if p, ok := breakers[name]; ok {
		return p
	}
	p := circuitbreaker.NewBuilder[any]().
		HandleIf(func(_ any, err error) bool {
			return retryableErr(name, err)
		}).
		WithFailureThreshold(5).
		WithDelay(30 * time.Second).
		Build()
	breakers[name] = p
	return p
}

func retryableErr(name string, err error) bool {
	if err == nil || errors.Is(err, ErrAbort) {
		return false
	}
	if TransportRetryable(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "http status 429") || strings.Contains(msg, "http status 5") {
		return true
	}
	switch name {
	case DepRegistry, DepDockerDaemon:
		return strings.Contains(msg, "timeout") ||
			strings.Contains(msg, "connection") ||
			strings.Contains(msg, "429") ||
			strings.Contains(msg, "temporarily") ||
			strings.Contains(msg, "daemon")
	case DepGitHub:
		return strings.Contains(msg, "github releases: http 429") ||
			strings.Contains(msg, "github releases: http 5") ||
			strings.Contains(msg, "github actions: http 429") ||
			strings.Contains(msg, "github actions: http 5") ||
			strings.Contains(msg, "github: tag ref http 5") ||
			strings.Contains(msg, "registry http 429") ||
			strings.Contains(msg, "registry http 5")
	case DepGHCR:
		return strings.Contains(msg, "registry http 429") ||
			strings.Contains(msg, "registry http 5")
	case DepCloudflare:
		return strings.Contains(msg, "cloudflare models: status 429") ||
			strings.Contains(msg, "cloudflare models: status 5")
	default:
		return false
	}
}

// WithDeadlineIfMissing sets a hop budget when ctx has no deadline.
func WithDeadlineIfMissing(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.Background(), func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func retryPolicyFor(name string) failsafe.Policy[any] {
	return retrypolicy.NewBuilder[any]().
		HandleIf(func(_ any, err error) bool {
			return retryableErr(name, err)
		}).
		WithMaxRetries(3).
		WithBackoff(1*time.Second, 8*time.Second).
		WithJitterFactor(0.2).
		Build()
}

func policies(name string) []failsafe.Policy[any] {
	var out []failsafe.Policy[any]
	if b := breakerFor(name); b != nil {
		out = append(out, b)
	}
	out = append(out, retryPolicyFor(name))
	return out
}

// Run executes fn under outbound retry (and breaker when name is a shared remote dep).
func Run(ctx context.Context, name string, fn func() error) error {
	if err := validateCtx(ctx); err != nil {
		return err
	}
	return failsafe.With(policies(name)...).
		WithContext(ctx).
		Run(fn)
}

// Do performs one HTTP request under outbound policies. req is cloned per attempt.
func Do(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	if err := validateCtx(ctx); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	if req == nil {
		return nil, fmt.Errorf("outbound: nil request")
	}
	var resp *http.Response
	err := failsafe.With(policies(name)...).
		WithContext(ctx).
		Run(func() error {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
				resp = nil
			}
			attempt, err := cloneRequest(ctx, req)
			if err != nil {
				return Abort(err)
			}
			r, err := client.Do(attempt)
			if err != nil {
				return err
			}
			retryStatus := name != DepHealth
			if retryStatus {
				if HTTPStatusPermanent(r.StatusCode) {
					resp = r
					return Abort(fmt.Errorf("http status %d", r.StatusCode))
				}
				if HTTPStatusRetryable(r.StatusCode) {
					_ = r.Body.Close()
					return fmt.Errorf("http status %d", r.StatusCode)
				}
			}
			resp = r
			return nil
		})
	if err != nil {
		if errors.Is(err, ErrAbort) && resp != nil {
			return resp, nil
		}
		return nil, err
	}
	return resp, nil
}

func cloneRequest(ctx context.Context, req *http.Request) (*http.Request, error) {
	clone := req.Clone(ctx)
	if clone == nil {
		return nil, fmt.Errorf("outbound: clone request")
	}
	return clone, nil
}
