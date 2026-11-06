package smoke

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSystemOneBillingSkipReadsTheStatus(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&statusError{what: "systemone", status: http.StatusPaymentRequired}, true},
		{fmt.Errorf("wrapped: %w", &statusError{what: "systemone", status: 400, body: "Use BYOK for this model"}), true},
		{&statusError{what: "systemone", status: http.StatusBadGateway, body: "upstream down"}, false},
		{errors.New("systemone status 402: said a string"), false},
		{nil, false},
	} {
		if got := isSystemOneBillingSkip(tc.err); got != tc.want {
			t.Fatalf("%v: got %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestRunKeepsTheProbeCauseInTheChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	_, err := Run(ctx, Options{BaseURL: srv.URL, Channels: []string{ChannelSystemOne}, RequireCF: true})
	var se *statusError
	if !errors.As(err, &se) || se.status != http.StatusPaymentRequired {
		t.Fatalf("want the probe's *statusError in the chain, got %v", err)
	}
}

func TestAPIKeyFrom(t *testing.T) {
	if got := apiKeyFrom(context.Background()); got != "" {
		t.Fatalf("no key: %q", got)
	}
	if got := apiKeyFrom(withAPIKey(context.Background(), " k ")); got != "k" {
		t.Fatalf("key: %q", got)
	}
}
