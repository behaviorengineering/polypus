package cloudflare

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// trackedBody records whether the caller closed the response body.
type trackedBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *trackedBody) Close() error {
	b.closed.Store(true)
	return nil
}

// statusTransport answers every request with status and the tracked body.
type statusTransport struct {
	status int
	body   *trackedBody
}

func (s statusTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: s.status, Status: http.StatusText(s.status), Header: http.Header{}, Body: s.body, Request: r}, nil
}

// TestPollClosesTheResponseADeadlineInterrupts: a poll retries a 503, the deadline
// ends the backoff, and outbound.Do hands back the last answer with the error. The
// client must close that answer, or the connection leaks.
func TestPollClosesTheResponseADeadlineInterrupts(t *testing.T) {
	body := &trackedBody{Reader: strings.NewReader("busy")}
	c, err := newClient("https://cf.invalid/accounts/acct/ai", "token", time.Now, SpeechDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	c.httpClient = &http.Client{Transport: statusTransport{status: http.StatusServiceUnavailable, body: body}}
	c.policies = outbound.NewPolicies()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err = c.PollBatch(ctx, "@cf/meta/llama-3.3-70b-instruct-fp8-fast", "cf-req-1")
	if derrors.CodeOf(err) != derrors.CodeTimeout {
		t.Fatalf("err = %v, want the deadline during retries", err)
	}
	if !body.closed.Load() {
		t.Fatal("the response Do returned with the error was not closed")
	}
}
