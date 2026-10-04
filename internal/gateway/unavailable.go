package gateway

import (
	"net/http"
	"strconv"

	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

type polypusFailureBody struct {
	Failure        polypusFailureMeta  `json:"failure"`
	CircuitBreaker *polypusBreakerMeta `json:"circuit_breaker,omitempty"`
}

type polypusFailureMeta struct {
	Layer    string `json:"layer"`
	Upstream string `json:"upstream"`
}

type polypusBreakerMeta struct {
	State string `json:"state"`
}

func writeUnavailableError(w http.ResponseWriter, err error, message, upstreamName string) {
	cls := upstream.ClassifyDialFailure(err, upstreamName)
	if upstream.Unavailable(err) {
		w.Header().Set("Retry-After", strconv.Itoa(int(upstream.OpenTimeout.Seconds())))
	}
	body := openaiErrorBody{
		Error: openaiError{
			Message: message,
			Type:    "unavailable_error",
			Code:    cls.Layer,
		},
		Polypus: &polypusFailureBody{
			Failure: polypusFailureMeta{
				Layer:    cls.Layer,
				Upstream: cls.Upstream,
			},
		},
	}
	if cls.BreakerState != "" {
		body.Polypus.CircuitBreaker = &polypusBreakerMeta{State: cls.BreakerState}
	}
	writeOpenAIJSON(w, http.StatusServiceUnavailable, body)
}
