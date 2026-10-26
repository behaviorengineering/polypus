package router

import (
	"net/http"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/maximhq/bifrost/core/schemas"
)

func bifrostTestErr(status int, errType, msg string) *schemas.BifrostError {
	e := &schemas.BifrostError{StatusCode: &status, Error: &schemas.ErrorField{Message: msg}}
	if errType != "" {
		e.Error.Type = &errType
	}
	return e
}

func TestAnthropicChatErrFallbackStatuses(t *testing.T) {
	for name, tc := range map[string]struct {
		berr *schemas.BifrostError
		want int
	}{
		"402 billing":          {bifrostTestErr(402, "billing_error", "Your credit balance is too low"), http.StatusServiceUnavailable},
		"400 credit balance":   {bifrostTestErr(400, "invalid_request_error", "Your credit balance is too low to access the API"), http.StatusServiceUnavailable},
		"529 overloaded":       {bifrostTestErr(529, "overloaded_error", "Overloaded"), http.StatusServiceUnavailable},
		"overloaded type only": {bifrostTestErr(500, "overloaded_error", "Overloaded"), http.StatusServiceUnavailable},
		"400 bad request":      {bifrostTestErr(400, "invalid_request_error", "tools.0: invalid schema"), http.StatusBadRequest},
		"401 bad key":          {bifrostTestErr(401, "authentication_error", "invalid x-api-key"), http.StatusUnauthorized},
		"403 permission":       {bifrostTestErr(403, "permission_error", "no access"), http.StatusUnauthorized},
		"404 model":            {bifrostTestErr(404, "not_found_error", "model not found"), http.StatusNotFound},
		"413 too large":        {bifrostTestErr(413, "request_too_large", "too large"), http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			err := anthropicChatErr(tc.berr)
			if err == nil {
				t.Fatal("expected a mapped error")
			}
			if got := derrors.HTTPStatus(err); got != tc.want {
				t.Fatalf("status: got %d want %d (%v)", got, tc.want, err)
			}
		})
	}
}

func TestAnthropicChatErrDefersRateLimitAndServerErrors(t *testing.T) {
	for _, status := range []int{429, 500, 502} {
		if err := anthropicChatErr(bifrostTestErr(status, "", "x")); err != nil {
			t.Fatalf("status %d should defer to bifrostErr, got %v", status, err)
		}
	}
	if anthropicChatErr(nil) != nil {
		t.Fatal("nil in, nil out")
	}
}

func TestChatBifrostErrScopesToAnthropic(t *testing.T) {
	t.Setenv(anthropicTestKeyEnv, "secret-key-at-least-twenty-ch")
	t.Setenv("OTHER_TEST_KEY", "secret-key-at-least-twenty-ch")
	gemini := anthropicBackend("")
	gemini.ID = "gemini_paid"
	gemini.Extension = config.ExtensionGemini
	gemini.Auth = config.BackendAuth{BearerEnv: "OTHER_TEST_KEY"}
	reg, err := NewRegistry(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"claude_team": anthropicBackend(""),
			"gemini_paid": gemini,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{reg: reg}
	berr := bifrostTestErr(400, "invalid_request_error", "bad")
	if got := derrors.HTTPStatus(c.chatBifrostErr("claude_team", berr, nil)); got != http.StatusBadRequest {
		t.Fatalf("claude 400: got %d", got)
	}
	if got := derrors.HTTPStatus(c.chatBifrostErr("gemini_paid", berr, nil)); got != http.StatusBadGateway {
		t.Fatalf("gemini keeps today's mapping: got %d", got)
	}
}
