package gateway

import (
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel/trace"

	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
)

type polypusFailureBody struct {
	Failure        *polypusFailureMeta `json:"failure,omitempty"`
	CircuitBreaker *polypusBreakerMeta `json:"circuit_breaker,omitempty"`
	Intervention   string              `json:"intervention,omitempty"`
}

type polypusFailureMeta struct {
	Layer    string `json:"layer"`
	Upstream string `json:"upstream"`
}

type polypusBreakerMeta struct {
	State string `json:"state"`
}

func (rs responder) writeUnavailableError(w http.ResponseWriter, err error, message, upstreamName string) {
	cls := upstream.ClassifyDialFailure(err, upstreamName)
	if upstream.Unavailable(err) {
		w.Header().Set("Retry-After", strconv.Itoa(int(upstream.OpenTimeout.Seconds())))
	}
	body := openaiErrorBody{
		Error: openaiError{
			Message: message,
			Type:    errTypeUnavailable,
			Code:    cls.Layer,
		},
		Polypus: &polypusFailureBody{
			Failure: &polypusFailureMeta{
				Layer:    cls.Layer,
				Upstream: cls.Upstream,
			},
		},
	}
	if cls.BreakerState != "" {
		body.Polypus.CircuitBreaker = &polypusBreakerMeta{State: cls.BreakerState}
	}
	rs.writeJSON(w, http.StatusServiceUnavailable, body)
}

// dialFailureLabels classifies a dial error with the gateway's breaker and upstream
// rules, for observability to record on the span.
func dialFailureLabels(err error, upstreamName string) observability.DialFailure {
	if err == nil {
		return observability.DialFailure{}
	}
	c := upstream.ClassifyDialFailure(err, upstreamName)
	return observability.DialFailure{
		Layer:        c.Layer,
		Upstream:     c.Upstream,
		BreakerState: c.BreakerState,
		HTTPStatus:   c.HTTPStatus,
	}
}

// endDialSpan ends a dial span, labelling a failure with dialFailureLabels.
func endDialSpan(span trace.Span, err error, upstreamName string) {
	observability.EndDialSpan(span, err, dialFailureLabels(err, upstreamName))
}
