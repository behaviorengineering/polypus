package upstream

import (
	"errors"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/sony/gobreaker"
)

func TestResolveUpstreamNameFromBreakerWrap(t *testing.T) {
	err := UnavailableError("edge_cf", gobreaker.ErrOpenState)
	got := ResolveUpstreamName(err, "")
	if got != "edge_cf" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveUpstreamNameHintWins(t *testing.T) {
	err := UnavailableError("other", gobreaker.ErrOpenState)
	got := ResolveUpstreamName(err, "hint_backend")
	if got != "hint_backend" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyDialFailureCloudflareBackendID(t *testing.T) {
	err := derrors.New(derrors.CodeUnavailable, "router.bifrost", "provider call").
		With("status", "502").
		With("cf_code", "3036")
	cls := ClassifyDialFailure(err, "my_cloudflare_backend")
	if cls.Layer != LayerCloudflare {
		t.Fatalf("layer=%q", cls.Layer)
	}
	if cls.Upstream != "my_cloudflare_backend" {
		t.Fatalf("upstream=%q", cls.Upstream)
	}
}

func TestClassifyDialFailureBifrostLeafStatus(t *testing.T) {
	err := derrors.New(derrors.CodeUnavailable, "router.bifrost", "provider call").
		With("status", "502")
	cls := ClassifyDialFailure(err, "lm_studio")
	if cls.Layer != LayerLeaf {
		t.Fatalf("layer=%q want leaf", cls.Layer)
	}
	if cls.HTTPStatus != 502 {
		t.Fatalf("status=%d", cls.HTTPStatus)
	}
}

func TestClassifyDialFailureBreakerUpstreamParsed(t *testing.T) {
	err := UnavailableError("cf_local", gobreaker.ErrOpenState)
	cls := ClassifyDialFailure(err, "")
	if cls.Upstream != "cf_local" {
		t.Fatalf("upstream=%q", cls.Upstream)
	}
	if cls.Layer != LayerPolypusBreaker {
		t.Fatalf("layer=%q", cls.Layer)
	}
}

func TestStatusFromTextEndingInStatusDoesNotPanic(t *testing.T) {
	cls := ClassifyDialFailure(errors.New("upstream said invalid status "), "claude_team")
	if cls.Upstream != "claude_team" {
		t.Fatalf("classification: %+v", cls)
	}
}
