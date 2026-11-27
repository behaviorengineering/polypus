package otelcol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestCustomNeedsReexport(t *testing.T) {
	t.Parallel()
	if customNeedsReexport(phoenixTraceCost{}) {
		t.Fatal("missing trace must not re-export")
	}
	if customNeedsReexport(phoenixTraceCost{traceFound: true, priced: true, promptTokens: 10}) {
		t.Fatal("priced hop must not re-export")
	}
	if !customNeedsReexport(phoenixTraceCost{traceFound: true, promptTokens: 10}) {
		t.Fatal("unpriced tokens must re-export after the model store refresh")
	}
}

func TestCostProbeError(t *testing.T) {
	t.Parallel()
	if err := costProbeError("custom leaf", phoenixTraceCost{}); err == nil || !strings.Contains(err.Error(), "not ingested") {
		t.Fatalf("missing trace: %v", err)
	}
	if err := costProbeError("custom leaf", phoenixTraceCost{traceFound: true, priced: true}); err != nil {
		t.Fatalf("priced hop: %v", err)
	}
	if err := costProbeError("custom leaf", phoenixTraceCost{traceFound: true}); err == nil || !strings.Contains(err.Error(), "missing token counts") {
		t.Fatalf("no tokens: %v", err)
	}
	if err := costProbeError("openai leaf", phoenixTraceCost{traceFound: true, promptTokens: 10}); err == nil || !strings.Contains(err.Error(), "cost is 0") {
		t.Fatalf("tokens without dollars: %v", err)
	}
}

func TestLLMCostSpanAttrs(t *testing.T) {
	t.Parallel()
	attrs := llmCostSpanAttrs("cf_local/@cf/polypus-cost-smoke-ab", customCostSmokeProvider)
	got := map[string]string{}
	var prompt, completion, total int64
	for _, a := range attrs {
		switch string(a.Key) {
		case "openinference.span.kind", "llm.model_name", "llm.provider", "smoke.probe":
			got[string(a.Key)] = a.Value.AsString()
		case "llm.token_count.prompt":
			prompt = a.Value.AsInt64()
		case "llm.token_count.completion":
			completion = a.Value.AsInt64()
		case "llm.token_count.total":
			total = a.Value.AsInt64()
		}
	}
	if got["openinference.span.kind"] != "LLM" || got["llm.provider"] != customCostSmokeProvider {
		t.Fatalf("identity: %+v", got)
	}
	if !strings.HasPrefix(got["llm.model_name"], customCostSmokePrefix) {
		t.Fatalf("model %q", got["llm.model_name"])
	}
	if prompt != smokePromptTokens || completion != smokeCompletionTokens || total != smokeTotalTokens {
		t.Fatalf("tokens prompt=%d completion=%d total=%d", prompt, completion, total)
	}
}

func TestPhoenixGraphQLTraceCost(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"data": {
			"projects": {
				"edges": [{
					"node": {
						"name": "default",
						"trace": {
							"costSummary": {"total": {"cost": 0.012}},
							"spans": {"edges": [{
								"node": {
									"tokenCountPrompt": 10,
									"tokenCountCompletion": 5,
									"costSummary": {"total": {"cost": 0.012}}
								}
							}]}
						}
					}
				}]
			}
		}
	}`)
	got, err := phoenixGraphQLTraceCost(body, "default")
	if err != nil {
		t.Fatal(err)
	}
	if !got.traceFound || !got.priced || got.promptTokens != 10 || got.completionTokens != 5 {
		t.Fatalf("got %+v", got)
	}

	empty, err := phoenixGraphQLTraceCost([]byte(`{
		"data": {"projects": {"edges": [{"node": {"name": "default", "trace": null}}]}}
	}`), "")
	if err != nil || empty.traceFound || empty.priced {
		t.Fatalf("missing trace: %+v err=%v", empty, err)
	}
}

func TestPhoenixCreateModelResult(t *testing.T) {
	t.Parallel()
	if err := phoenixCreateModelResult([]byte(`{"data":{"createModel":{"model":{"id":"m1"}}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := phoenixCreateModelResult([]byte(`{"errors":[{"message":"duplicate name"}]}`)); err == nil || !strings.Contains(err.Error(), "duplicate name") {
		t.Fatalf("graphql error: %v", err)
	}
	if err := phoenixCreateModelResult([]byte(`{"data":{"createModel":null}}`)); err == nil || !strings.Contains(err.Error(), "no model") {
		t.Fatalf("empty payload: %v", err)
	}
}

func TestPhoenixCreateModelPostsInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !strings.Contains(req.Query, "createModel") {
			http.Error(w, "wrong query", http.StatusBadRequest)
			return
		}
		raw, err := json.Marshal(req.Variables)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var vars struct {
			Input phoenixCreateModelInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &vars); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if vars.Input.Provider != customCostSmokeProvider || vars.Input.NamePattern == "" || len(vars.Input.Costs) != 2 {
			http.Error(w, "bad input", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"createModel": map[string]any{"model": map[string]string{"id": "m1"}}},
		})
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := phoenixCreateModel(ctx, CollectorOptions{PhoenixBaseURL: srv.URL}, phoenixCreateModelInput{
		Name:        "polypus-cost-smoke-test",
		Provider:    customCostSmokeProvider,
		NamePattern: `^cf_local/@cf/polypus-cost-smoke-test$`,
		Costs: []phoenixTokenPriceInput{
			{TokenType: "input", Kind: "PROMPT", CostPerMillionTokens: 1},
			{TokenType: "output", Kind: "COMPLETION", CostPerMillionTokens: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWaitProbeSucceedsOnLaterPass(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n := 0
	if err := waitProbe(ctx, time.Millisecond, func() error {
		n++
		if n < 3 {
			return errors.New("not yet")
		}
		return nil
	}, "unavailable", "context ended"); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("probes=%d", n)
	}
}

func TestWaitProbeUnavailableWhenDeadlineTooClose(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := waitProbe(ctx, time.Second, func() error {
		return errors.New("still missing")
	}, "routing not confirmed before the deadline", "context ended before routing was confirmed")
	if derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("want unavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "still missing") {
		t.Fatalf("must wrap the probe error: %v", err)
	}
}

func TestPhoenixTraceCostGraphQLRoundTrip(t *testing.T) {
	const traceID = "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"projects": map[string]any{
					"edges": []map[string]any{{
						"node": map[string]any{
							"name": "default",
							"trace": map[string]any{
								"costSummary": map[string]any{"total": map[string]any{"cost": 1.5}},
								"spans": map[string]any{"edges": []map[string]any{{
									"node": map[string]any{
										"tokenCountPrompt":     10,
										"tokenCountCompletion": 5,
										"costSummary":          map[string]any{"total": map[string]any{"cost": 1.5}},
									},
								}}},
							},
						},
					}},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := phoenixTraceCostGraphQL(ctx, CollectorOptions{PhoenixBaseURL: srv.URL, PhoenixProject: "default"}, traceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := costProbeError("custom leaf", got); err != nil {
		t.Fatal(err)
	}
}
