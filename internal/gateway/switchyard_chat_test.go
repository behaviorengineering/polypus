package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// switchyardRouterConfig is scriptedRouterConfig plus the composed router
// "investigator", which the gateway sends to Switchyard.
func switchyardRouterConfig() config.RouterConfig {
	cfg := scriptedRouterConfig()
	cfg.Routers = map[string]config.NamedRouter{
		"investigator": {
			Name:       "investigator",
			Capability: config.CapChat,
			Route: config.RouterRoute{
				Type:                config.RouteStageRouter,
				Capable:             "leaf/a",
				Efficient:           "leaf/b",
				Picker:              config.PickerEfficientFirst,
				ConfidenceThreshold: 0.5,
			},
		},
	}
	return cfg
}

const investigatorModel = "router/investigator"

func TestSwitchyardChatGoesThroughBifrost(t *testing.T) {
	sr := &scriptedRouter{chat: func(context.Context, router.Call) ([]byte, error) {
		return []byte(`{"model":"leaf/b","choices":[{"index":0,"message":{"role":"assistant","content":"via-switchyard"}}]}`), nil
	}}
	h := newScriptedGateway(t, sr, switchyardRouterConfig())
	rec := postChat(t, h, investigatorModel, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	calls := sr.recorded()
	if len(calls) != 1 {
		t.Fatalf("calls=%+v", calls)
	}
	call := calls[0]
	if call.BackendID != router.ProviderSwitchyard || call.Model != "investigator" {
		t.Fatalf("the hop must name Switchyard and the router: %+v", call)
	}
	var sent map[string]any
	if err := json.Unmarshal(call.Body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["model"] != investigatorModel {
		t.Fatalf("Switchyard must get the body as the client sent it: %s", call.Body)
	}
	if call.Timeout <= 0 {
		t.Fatal("a non-stream Switchyard hop must carry its budget")
	}
}

func TestSwitchyardChatRecordsSelectedModel(t *testing.T) {
	const chunk = `{"id":"1","object":"chat.completion.chunk","model":"leaf/from-body","choices":[{"index":0,"delta":{"content":"hi"}}]}`
	const completion = `{"model":"leaf/from-body","choices":[{"index":0,"message":{"role":"assistant","content":"hi"}}]}`
	cases := []struct {
		name   string
		stream bool
		header http.Header
		want   string
	}{
		{name: "non-stream header", header: http.Header{"X-Model-Router-Selected-Model": {"leaf/from-header"}}, want: "leaf/from-header"},
		{name: "non-stream body", want: "leaf/from-body"},
		{name: "stream header", stream: true, header: http.Header{"X-Switchyard-Selected-Model": {"leaf/from-header"}}, want: "leaf/from-header"},
		{name: "stream body", stream: true, want: "leaf/from-body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exporter := installTestTracer(t)
			sr := &scriptedRouter{
				header: tc.header,
				stream: streamOf(chunk),
				chat: func(context.Context, router.Call) ([]byte, error) {
					return []byte(completion), nil
				},
			}
			h := newScriptedGateway(t, sr, switchyardRouterConfig())
			rec := postChat(t, h, investigatorModel, tc.stream)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if got := spanAttr(exporter, "polypus.router", "polypus.downstream_model"); got != tc.want {
				t.Fatalf("downstream model on the router span = %q, want %q", got, tc.want)
			}
			for key := range tc.header {
				if rec.Header().Get(key) != "leaf/from-header" {
					t.Fatalf("the selected-model header must reach the caller: %v", rec.Header())
				}
			}
		})
	}
}

func TestSwitchyardChatRateLimitKeepsRetryAfter(t *testing.T) {
	sr := &scriptedRouter{chat: func(context.Context, router.Call) ([]byte, error) {
		return nil, rateLimited("11")
	}}
	h := newScriptedGateway(t, sr, switchyardRouterConfig())
	rec := postChat(t, h, investigatorModel, false)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "11" {
		t.Fatalf("a Switchyard 429 must reach the caller with Retry-After: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestSwitchyardChatNotRegisteredFails(t *testing.T) {
	sr := &scriptedRouter{unregistered: map[string]bool{router.ProviderSwitchyard: true}}
	h := newScriptedGateway(t, sr, switchyardRouterConfig())
	rec := postChat(t, h, investigatorModel, false)
	if rec.Code != http.StatusBadRequest || !jsonErrorCode(t, rec.Body.Bytes(), string(derrors.CodeFailedPrecondition)) {
		t.Fatalf("a composed router without Switchyard registered fails closed: %d %s", rec.Code, rec.Body.String())
	}
	if len(sr.recorded()) != 0 {
		t.Fatal("nothing may be dialled")
	}
}

// installTestTracer records spans in memory for the test, then restores the
// process tracer provider.
func installTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	return exporter
}

// spanAttr returns attribute key of the first ended span named name.
func spanAttr(exporter *tracetest.InMemoryExporter, name, key string) string {
	for _, s := range exporter.GetSpans() {
		if s.Name != name {
			continue
		}
		for _, kv := range s.Attributes {
			if string(kv.Key) == key {
				return kv.Value.AsString()
			}
		}
	}
	return ""
}

// jsonErrorCode reports whether an OpenAI error body carries code.
func jsonErrorCode(t *testing.T, body []byte, code string) bool {
	t.Helper()
	var out openaiErrorBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("error body: %v (%s)", err, body)
	}
	return out.Error.Code == code
}
