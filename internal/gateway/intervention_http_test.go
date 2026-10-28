package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
)

func TestWriteErrorJSONInterventionHeaderAndBody(t *testing.T) {
	err := derrors.New(derrors.CodeNotReady, "gateway.serveEmbeddings", "no embed backend configured").
		With(derrors.FieldIntervention, string(observability.InterventionEmbedCapabilityMissing))
	rec := httptest.NewRecorder()
	testShared.writeErrorJSON(rec, http.StatusServiceUnavailable, err, "")
	if got := rec.Header().Get(config.InterventionHeader); got != "embed_capability_missing" {
		t.Fatalf("header=%q", got)
	}
	var body openaiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Polypus == nil || body.Polypus.Intervention != "embed_capability_missing" {
		t.Fatalf("polypus=%+v", body.Polypus)
	}
	if body.Polypus.Failure != nil {
		t.Fatalf("expected no failure block, got %+v", body.Polypus.Failure)
	}
}

func TestMergeReasoningIntoContentSetsIntervention(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := observability.WithInterventionState(context.Background())
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"index":0,"message":{"reasoning_content":"hidden","content":""}}]}`)),
	}
	d := chatDial{}
	if err := d.writeResponse(rec, req.WithContext(ctx), resp, "test"); err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get(config.InterventionHeader); got != "merge_reasoning_into_content" {
		t.Fatalf("header=%q", got)
	}
}
