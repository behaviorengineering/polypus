package gateway

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// scriptedRouter is a fake Bifrost router whose answers each test scripts. Every
// backend counts as registered unless listed in unregistered. On success it hands
// header to the call's OnResponseHeader hook, as the real router does with the
// provider's response headers.
type scriptedRouter struct {
	reg          *router.Registry
	unregistered map[string]bool
	header       http.Header

	chat     func(ctx context.Context, call router.Call) ([]byte, error)
	stream   func(ctx context.Context, call router.Call) (<-chan router.ChatStreamChunk, <-chan error, error)
	embed    func(ctx context.Context, call router.Call) ([]byte, error)
	decision func(ctx context.Context, call router.Call) ([]byte, error)

	mu    sync.Mutex
	calls []router.Call
}

func (s *scriptedRouter) Registry() *router.Registry { return s.reg }

func (s *scriptedRouter) UsesBifrost(id string) bool { return !s.unregistered[id] }

func (s *scriptedRouter) record(call router.Call) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

// recorded returns the calls the router received, in order.
func (s *scriptedRouter) recorded() []router.Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]router.Call(nil), s.calls...)
}

func (s *scriptedRouter) answered(call router.Call, err error) {
	if err == nil && s.header != nil && call.OnResponseHeader != nil {
		call.OnResponseHeader(s.header)
	}
}

func (s *scriptedRouter) ChatCompletionRaw(ctx context.Context, call router.Call) ([]byte, error) {
	s.record(call)
	if s.chat == nil {
		return nil, derrors.New(derrors.CodeUnimplemented, "scriptedRouter.chat", "not scripted")
	}
	out, err := s.chat(ctx, call)
	s.answered(call, err)
	return out, err
}

func (s *scriptedRouter) ChatCompletionStreamRaw(ctx context.Context, call router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
	s.record(call)
	if s.stream == nil {
		return nil, nil, derrors.New(derrors.CodeUnimplemented, "scriptedRouter.stream", "not scripted")
	}
	chunks, errs, err := s.stream(ctx, call)
	s.answered(call, err)
	return chunks, errs, err
}

func (s *scriptedRouter) EmbeddingRaw(ctx context.Context, call router.Call) ([]byte, error) {
	s.record(call)
	if s.embed == nil {
		return nil, derrors.New(derrors.CodeUnimplemented, "scriptedRouter.embed", "not scripted")
	}
	return s.embed(ctx, call)
}

func (s *scriptedRouter) SystemOneDecisionRaw(ctx context.Context, call router.Call) ([]byte, error) {
	s.record(call)
	if s.decision == nil {
		return nil, derrors.New(derrors.CodeUnimplemented, "scriptedRouter.decision", "not scripted")
	}
	return s.decision(ctx, call)
}

func (s *scriptedRouter) Synthesize(context.Context, router.SpeechRequest) ([]byte, error) {
	return nil, derrors.New(derrors.CodeUnimplemented, "scriptedRouter.Synthesize", "not scripted")
}

func (s *scriptedRouter) Transcribe(context.Context, router.TranscriptionRequest) ([]byte, string, error) {
	return nil, "", derrors.New(derrors.CodeUnimplemented, "scriptedRouter.Transcribe", "not scripted")
}

// streamOf scripts a stream that sends chunks, then ends cleanly.
func streamOf(chunks ...string) func(context.Context, router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
	return func(context.Context, router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
		out := make(chan router.ChatStreamChunk, len(chunks))
		errs := make(chan error)
		for _, c := range chunks {
			out <- router.ChatStreamChunk{OpenAIJSON: []byte(c)}
		}
		close(out)
		close(errs)
		return out, errs, nil
	}
}

// stallingStream scripts a stream that sends chunks, then goes quiet until its
// context ends, as the real router does when the provider stalls.
func stallingStream(chunks ...string) func(context.Context, router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
	return func(ctx context.Context, _ router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
		out := make(chan router.ChatStreamChunk)
		errs := make(chan error, 1)
		go func() {
			defer close(out)
			defer close(errs)
			for _, c := range chunks {
				select {
				case out <- router.ChatStreamChunk{OpenAIJSON: []byte(c)}:
				case <-ctx.Done():
					errs <- derrors.Wrap(ctx.Err(), derrors.CodeCanceled, "scriptedRouter.stream", "context done")
					return
				}
			}
			<-ctx.Done()
			errs <- derrors.Wrap(ctx.Err(), derrors.CodeCanceled, "scriptedRouter.stream", "context done")
		}()
		return out, errs, nil
	}
}

// scriptedRouterConfig is one leaf backend, "leaf", serving chat and embeddings, with
// short chat and stream idle budgets so timeout tests run fast.
func scriptedRouterConfig() config.RouterConfig {
	timeouts := config.DefaultTimeouts()
	timeouts.Min = 50 * time.Millisecond
	timeouts.Chat = 200 * time.Millisecond
	timeouts.StreamIdle = 200 * time.Millisecond
	return config.RouterConfig{
		Chat:     config.CapabilityBackend{Enabled: true, Default: "leaf"},
		Embed:    config.CapabilityBackend{Enabled: true, Default: "leaf"},
		Policy:   config.DefaultRouterPolicy(),
		Timeouts: timeouts,
		Backends: map[string]config.BackendDef{
			"leaf": {
				ID:           "leaf",
				BaseURL:      "http://127.0.0.1:9",
				Capabilities: []config.Capability{config.CapChat, config.CapEmbed},
			},
		},
	}
}

// newScriptedGateway builds a gateway over sr with cfg as its routing table.
func newScriptedGateway(t *testing.T, sr *scriptedRouter, cfg config.RouterConfig, extra ...HandlerOption) http.Handler {
	t.Helper()
	reg, err := router.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sr.reg = reg
	extra = append(extra, WithAdminStateDir(t.TempDir()))
	return newTestGateway(t, config.ServeOptions{BackendURL: "http://127.0.0.1:9"}, sr, extra...)
}
