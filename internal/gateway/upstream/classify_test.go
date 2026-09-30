package upstream

import (
	"errors"
	"fmt"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/sony/gobreaker"
)

func TestClassifyDialFailureBreaker(t *testing.T) {
	err := fmt.Errorf("upstream cf_local unavailable: %w", gobreaker.ErrOpenState)
	cls := ClassifyDialFailure(err, "cf_local")
	if cls.Layer != LayerPolypusBreaker {
		t.Fatalf("layer=%s", cls.Layer)
	}
	if cls.BreakerState != "open" {
		t.Fatalf("state=%s", cls.BreakerState)
	}
}

func TestClassifyDialFailureCloudflareStatus(t *testing.T) {
	err := derrors.New(derrors.CodeUnavailable, "router.bifrost", "provider call").
		With("status", "429")
	cls := ClassifyDialFailure(err, "cf_local")
	if cls.Layer != LayerCloudflare {
		t.Fatalf("layer=%s", cls.Layer)
	}
	if cls.HTTPStatus != 429 {
		t.Fatalf("status=%d", cls.HTTPStatus)
	}
}

func TestClassifyDialFailureSwitchyard(t *testing.T) {
	err := errors.New("polypus: switchyard unavailable")
	cls := ClassifyDialFailure(err, NameSwitchyard)
	if cls.Layer != LayerSwitchyard {
		t.Fatalf("layer=%s", cls.Layer)
	}
}
