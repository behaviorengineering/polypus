package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
)

type openaiErrorBody struct {
	Error   openaiError         `json:"error"`
	Polypus *polypusFailureBody `json:"polypus,omitempty"`
}

// OpenAI error types clients switch on.
const (
	errTypeInvalidRequest = "invalid_request_error"
	errTypeAuthentication = "authentication_error"
	errTypeNotFound       = "not_found_error"
	errTypeAPI            = "api_error"
)

type openaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code"`
}

func (s *shared) writeRateLimitError(w http.ResponseWriter, err error) bool {
	if err == nil || derrors.CodeOf(err) != derrors.CodeRateLimited {
		return false
	}
	if ra := derrors.Field(err, ratelimit.FieldRetryAfter); ra != "" {
		w.Header().Set("Retry-After", ra)
	}
	if rl := derrors.Field(err, ratelimit.FieldRateLimit); rl != "" {
		w.Header().Set("Ratelimit", rl)
	}
	if ray := derrors.Field(err, ratelimit.FieldCFRay); ray != "" {
		w.Header().Set("Cf-Ray", ray)
	}
	setRateLimitExtraHeaders(w, err)
	s.writeJSON(w, http.StatusTooManyRequests, openaiErrorBody{Error: openaiRateLimitError(err)})
	return true
}

func setRateLimitExtraHeaders(w http.ResponseWriter, err error) {
	derrors.Walk(err, func(de *derrors.Error) bool {
		for k, v := range de.Fields() {
			if !strings.HasPrefix(k, ratelimit.FieldHeaderPrefix) {
				continue
			}
			name := strings.TrimPrefix(k, ratelimit.FieldHeaderPrefix)
			if name == "" {
				continue
			}
			if v = strings.TrimSpace(v); v != "" {
				w.Header().Set(name, v)
			}
		}
		return true
	})
}

func openaiRateLimitBody(status int, header http.Header, body []byte) ([]byte, bool) {
	rl := ratelimit.Classify("gateway.proxyChat", status, header, body)
	if rl == nil {
		return nil, false
	}
	out, err := json.Marshal(openaiErrorBody{Error: openaiRateLimitError(rl)})
	if err != nil {
		return nil, false
	}
	return out, true
}

func openaiRateLimitError(err error) openaiError {
	code := derrors.Field(err, ratelimit.FieldCFCode)
	if code == "" {
		code = "rate_limited"
	}
	return openaiError{
		Message: rateLimitClientMessage(err),
		Type:    "rate_limit_error",
		Code:    code,
	}
}

func rateLimitClientMessage(err error) string {
	msg := ""
	derrors.Walk(err, func(de *derrors.Error) bool {
		if de.Code() != derrors.CodeRateLimited {
			return true
		}
		if m := strings.TrimSpace(de.Message()); m != "" {
			msg = m // the innermost rate-limit message wins.
		}
		return true
	})
	if msg != "" {
		return msg
	}
	return "rate limited by upstream"
}
