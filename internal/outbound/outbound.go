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

	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// ErrAbort marks a non-retryable outbound failure.
var ErrAbort = errors.New("outbound: non-retryable")

// Dependency names for breaker keys.
const (
	depGitHub       = "github"
	depGHCR         = "ghcr"
	depRegistry     = "registry"
	DepHealth       = "health"
	DepDockerDaemon = "docker-daemon"
	DepCloudflare   = "cloudflare"
	DepGemini       = "gemini"
	DepAnthropic    = "anthropic"
	DepOpenRouter   = "openrouter"
	DepSmoke        = "smoke"
	DepGateway      = "gateway"
)

// Abort wraps err so failsafe stops retrying.
func Abort(err error) error {
	if err == nil {
		return fmt.Errorf("%w: nil", ErrAbort)
	}
	return fmt.Errorf("%w: %w", ErrAbort, err)
}

// httpStatusRetryable reports whether an HTTP status should be retried.
func httpStatusRetryable(status int) bool {
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

// transportRetryable reports whether a transport-level error should be retried.
func transportRetryable(err error) bool {
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
		return derrors.New(derrors.CodeInvalid, "outbound", "context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return derrors.New(derrors.CodeFailedPrecondition, "outbound", "missing deadline")
	}
	return nil
}

// Policies holds the circuit breakers for a set of outbound dependencies. A gateway
// builds its own, so tests and gateways never share breaker state; calls without
// one use the process default.
type Policies struct {
	mu       sync.Mutex
	breakers map[string]failsafe.Policy[any]
}

// NewPolicies returns an empty policy set.
func NewPolicies() *Policies {
	return &Policies{breakers: map[string]failsafe.Policy[any]{}}
}

// defaultPolicies serves the package-level Run, Do and DoOnce.
var defaultPolicies = NewPolicies()

// Scoped names one instance of a dependency, such as one backend of a provider kind,
// so it gets its own breaker: Scoped(DepAnthropic, "claude_team").
func Scoped(dep, scope string) string {
	if strings.TrimSpace(scope) == "" {
		return dep
	}
	return dep + "/" + scope
}

// depOf is the dependency kind of a name built by Scoped.
func depOf(name string) string {
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[:i]
	}
	return name
}

func (p *Policies) orDefault() *Policies {
	if p == nil {
		return defaultPolicies
	}
	return p
}

func (p *Policies) breakerFor(name string) failsafe.Policy[any] {
	switch depOf(name) {
	case depGitHub, depGHCR, depRegistry, DepCloudflare, DepGemini, DepAnthropic, DepOpenRouter:
	default:
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if b, ok := p.breakers[name]; ok {
		return b
	}
	b := circuitbreaker.NewBuilder[any]().
		HandleIf(func(_ any, err error) bool {
			return retryableErr(name, err)
		}).
		WithFailureThreshold(5).
		WithDelay(30 * time.Second).
		Build()
	p.breakers[name] = b
	return b
}

func retryableErr(name string, err error) bool {
	if err == nil || errors.Is(err, ErrAbort) {
		return false
	}
	if transportRetryable(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "http status 429") || strings.Contains(msg, "http status 5") {
		return true
	}
	switch depOf(name) {
	case depRegistry, DepDockerDaemon:
		return strings.Contains(msg, "timeout") ||
			strings.Contains(msg, "connection") ||
			strings.Contains(msg, "429") ||
			strings.Contains(msg, "temporarily") ||
			strings.Contains(msg, "daemon")
	case depGitHub:
		return strings.Contains(msg, "github releases: http 429") ||
			strings.Contains(msg, "github releases: http 5") ||
			strings.Contains(msg, "github actions: http 429") ||
			strings.Contains(msg, "github actions: http 5") ||
			strings.Contains(msg, "github: tag ref http 5") ||
			strings.Contains(msg, "registry http 429") ||
			strings.Contains(msg, "registry http 5")
	case depGHCR:
		return strings.Contains(msg, "registry http 429") ||
			strings.Contains(msg, "registry http 5")
	case DepCloudflare:
		return strings.Contains(msg, "cloudflare models: status 429") ||
			strings.Contains(msg, "cloudflare models: status 5")
	case DepGemini:
		return strings.Contains(msg, "gemini models: status 429") ||
			strings.Contains(msg, "gemini models: status 5")
	case DepAnthropic:
		return strings.Contains(msg, "anthropic models: status 429") ||
			strings.Contains(msg, "anthropic models: status 5")
	default:
		return false
	}
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

func (p *Policies) policies(name string) []failsafe.Policy[any] {
	return append(p.breakerOnly(name), retryPolicyFor(name))
}

// breakerOnly is the policy set for a request that must not be retried.
func (p *Policies) breakerOnly(name string) []failsafe.Policy[any] {
	if b := p.breakerFor(name); b != nil {
		return []failsafe.Policy[any]{b}
	}
	return nil
}

// Run executes fn under outbound retry (and breaker when name is a shared remote dep),
// with the process default policies.
func Run(ctx context.Context, name string, fn func() error) error {
	return defaultPolicies.Run(ctx, name, fn)
}

// Do sends req under the process default policies; see Policies.Do.
func Do(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	return defaultPolicies.Do(ctx, name, client, req)
}

// DoOnce sends req once under the process default policies; see Policies.DoOnce.
func DoOnce(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	return defaultPolicies.DoOnce(ctx, name, client, req)
}

// Run executes fn under retry and the breaker for name.
func (p *Policies) Run(ctx context.Context, name string, fn func() error) error {
	if err := validateCtx(ctx); err != nil {
		return err
	}
	return failsafe.With(p.orDefault().policies(name)...).
		WithContext(ctx).
		Run(fn)
}

// Do performs one HTTP request under retry and the breaker for name. req is cloned
// per attempt. When every attempt ends in an HTTP status (permanent, or retryable
// with retries spent), Do returns the last response with a nil error for the caller
// to map.
func (p *Policies) Do(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	p = p.orDefault()
	if err := validateCtx(ctx); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, derrors.New(derrors.CodeInvalid, "outbound", "http client is required")
	}
	if req == nil {
		return nil, derrors.New(derrors.CodeInvalid, "outbound", "nil request")
	}
	pol := p.policies(name)
	if !replayable(req) {
		// A body without GetBody is consumed by the first attempt; retrying would
		// send it empty, so this request gets one attempt.
		pol = p.breakerOnly(name)
	}
	var resp *http.Response
	err := failsafe.With(pol...).
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
			r, err := client.Do(attempt) //nolint:gosec // G704: targets are operator-configured backend and registry URLs
			if err != nil {
				return err
			}
			retryStatus := name != DepHealth
			if retryStatus {
				if HTTPStatusPermanent(r.StatusCode) {
					resp = r
					return Abort(fmt.Errorf("http status %d", r.StatusCode))
				}
				if httpStatusRetryable(r.StatusCode) {
					// Kept until the next attempt closes it, so the caller still gets
					// the last upstream answer (status, body, Retry-After) when
					// retries run out.
					resp = r
					return fmt.Errorf("http status %d", r.StatusCode)
				}
			}
			resp = r
			return nil
		})
	if err != nil {
		if resp != nil {
			return resp, nil
		}
		return nil, dialError(name, err)
	}
	return resp, nil
}

// DoOnce sends req exactly once under the breaker for name, with no retry. Use it
// for paid or non-idempotent calls (inference, batch submit), whose upstream status
// and Retry-After the caller acts on. Like Do, it fails closed without a deadline.
func (p *Policies) DoOnce(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	p = p.orDefault()
	if err := validateCtx(ctx); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, derrors.New(derrors.CodeInvalid, "outbound", "http client is required")
	}
	if req == nil {
		return nil, derrors.New(derrors.CodeInvalid, "outbound", "nil request")
	}
	var resp *http.Response
	err := failsafe.With(p.breakerOnly(name)...).
		WithContext(ctx).
		Run(func() error {
			r, err := client.Do(req.WithContext(ctx)) //nolint:gosec // G704: targets are operator-configured backend URLs
			if err != nil {
				return err
			}
			resp = r
			return nil
		})
	if err != nil {
		return nil, dialError(name, err)
	}
	return resp, nil
}

// dialError types a transport or breaker failure as CodeUnavailable for dep name,
// keeping any domain code already in the chain.
func dialError(name string, err error) error {
	return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), "outbound."+name, "dial")
}

// cloneRequest copies req for one attempt. Clone shares the body, so a replayable
// body is reopened from GetBody for every attempt.
func cloneRequest(ctx context.Context, req *http.Request) (*http.Request, error) {
	clone := req.Clone(ctx)
	if clone == nil {
		return nil, fmt.Errorf("outbound: clone request")
	}
	if req.GetBody != nil && req.Body != nil && req.Body != http.NoBody {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("outbound: reopen request body: %w", err)
		}
		clone.Body = body
	}
	return clone, nil
}

// replayable reports whether every attempt can send the full body.
func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}
