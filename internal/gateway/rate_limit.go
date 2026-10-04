package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

type openaiErrorBody struct {
	Error   openaiError         `json:"error"`
	Polypus *polypusFailureBody `json:"polypus,omitempty"`
}

type openaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func writeRateLimitError(w http.ResponseWriter, err error) bool {
	if err == nil || derrors.CodeOf(err) != derrors.CodeRateLimited {
		return false
	}
	if ra := derrors.Field(err, cloudflare.FieldRetryAfter); ra != "" {
		w.Header().Set("Retry-After", ra)
	}
	if rl := derrors.Field(err, cloudflare.FieldRateLimit); rl != "" {
		w.Header().Set("Ratelimit", rl)
	}
	if ray := derrors.Field(err, cloudflare.FieldCFRay); ray != "" {
		w.Header().Set("Cf-Ray", ray)
	}
	writeOpenAIJSON(w, http.StatusTooManyRequests, openaiErrorBody{Error: openaiRateLimitError(err)})
	return true
}

func openaiRateLimitBody(status int, header http.Header, body []byte) ([]byte, bool) {
	rl := cloudflare.ClassifyRateLimit("cloudflare.workers", status, header, body)
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
	code := derrors.Field(err, cloudflare.FieldCFCode)
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
	for e := err; e != nil; {
		if de, ok := e.(*derrors.Error); ok && de != nil {
			if de.Code() == derrors.CodeRateLimited {
				if m := strings.TrimSpace(de.Message()); m != "" {
					msg = m
				}
			}
			e = de.Unwrap()
			continue
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	if msg != "" {
		return msg
	}
	return "rate limited by upstream"
}
