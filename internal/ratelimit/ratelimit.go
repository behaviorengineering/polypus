// Package ratelimit is the provider-neutral shape of an upstream rate limit. Each client
// classifies its throttled responses into a CodeRateLimited error carrying these
// fields, and the gateway turns the fields back into response headers. Cloudflare is
// the one upstream that also signals throttles by code in the body (3036, 3040, 1015):
// Classify treats a structured code (errors[].code) as a rate limit at any status,
// and reads a code in free text only to label an answer that is already a 429.
package ratelimit

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// Cloudflare Workers AI and edge rate-limit codes, and the error fields.
const (
	WorkersQuotaCode    = "3036"
	WorkersCapacityCode = "3040"
	EdgeBlockCode       = "1015"

	FieldStatus       = "status"
	FieldCFCode       = "cf_code"
	FieldRetryAfter   = "retry_after"
	FieldRateLimit    = "ratelimit"
	FieldCFRay        = "cf_ray"
	FieldKind         = "limit_kind"
	FieldHeaderPrefix = "hdr:"

	KindQuota    = "quota"
	KindCapacity = "capacity"
	KindEdge     = "edge"
	KindRequest  = "request"
)

var (
	code3036 = regexp.MustCompile(`\b3036\b`)
	code3040 = regexp.MustCompile(`\b3040\b`)
	code1015 = regexp.MustCompile(`\b1015\b`)
)

type workersErrorEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	} `json:"errors"`
}

// Classify returns a CodeRateLimited error when the status is 429, or when the
// Cloudflare error body carries a throttle code in its structured errors[].code field
// (or a hint is exactly such a code, as a provider error code is). A throttle code
// that only appears in free text (a message, an HTML page) labels a 429 but never
// turns another status into a rate limit, so a 5xx that mentions 3040 stays an
// upstream failure. ok is false, with a nil error, when the response is not a rate
// limit. op names
// the caller (and so the backend) that saw the response. Use it for Cloudflare and
// for paths that may relay Cloudflare (Switchyard, proxies).
func Classify(op string, status int, header http.Header, body []byte, hints ...string) (*derrors.Error, bool) {
	env := decodeWorkersEnvelope(body)
	cfCode := structuredCode(env, hints)
	if cfCode == "" && status == http.StatusTooManyRequests {
		cfCode = workersCodeFromText(strings.Join(hints, "\n") + "\n" + string(body))
	}
	return classify(op, response{status: status, header: header, env: env, hints: hints}, &cloudflareSignal{code: cfCode})
}

// ClassifyStatus is Classify for a backend that is not Cloudflare: only a 429 is a
// rate limit, Cloudflare codes in the body mean nothing, and the error carries none
// of the Cloudflare fields (limit_kind, cf_ray) that mark a failure as Cloudflare's.
// ok is false, with a nil error, for any other status.
func ClassifyStatus(op string, status int, header http.Header, body []byte, hints ...string) (*derrors.Error, bool) {
	if status != http.StatusTooManyRequests {
		return nil, false
	}
	return classify(op, response{status: status, header: header, env: decodeWorkersEnvelope(body), hints: hints}, nil)
}

// response is the upstream answer classify reads, with its body decoded once.
type response struct {
	status int
	header http.Header
	env    workersErrorEnvelope
	hints  []string
}

// cloudflareSignal marks a Cloudflare backend and carries the throttle code found in
// its answer ("" for none). A nil signal is any other backend.
type cloudflareSignal struct {
	code string
}

func classify(op string, r response, cf *cloudflareSignal) (*derrors.Error, bool) {
	if strings.TrimSpace(op) == "" {
		op = "ratelimit.Classify"
	}
	cloudflare := cf != nil
	cfCode := ""
	if cloudflare {
		cfCode = cf.code
	}
	status, header := r.status, r.header

	if status != http.StatusTooManyRequests && cfCode == "" {
		return nil, false
	}
	if status == 0 {
		status = http.StatusTooManyRequests
	}

	msg := firstWorkersMessage(r.env, r.hints...)
	kind := KindRequest
	switch cfCode {
	case WorkersQuotaCode:
		kind = KindQuota
		msg = annotateQuota(msg)
	case WorkersCapacityCode:
		kind = KindCapacity
		msg = annotateCapacity(msg)
	case EdgeBlockCode:
		kind = KindEdge
		msg = annotateEdge(msg)
	default:
		if msg == "" {
			msg = "rate limited by upstream"
		}
	}

	err := derrors.New(derrors.CodeRateLimited, op, msg).
		With(FieldStatus, strconv.Itoa(status))
	if cloudflare {
		err = err.With(FieldKind, kind)
	}
	if cfCode != "" {
		err = err.With(FieldCFCode, cfCode)
	}
	if header != nil {
		if v := strings.TrimSpace(header.Get("Retry-After")); v != "" {
			err = err.With(FieldRetryAfter, v)
		}
		if v := firstHeader(header, "RateLimit", "Rate-Limit"); v != "" {
			err = err.With(FieldRateLimit, v)
		}
		if v := strings.TrimSpace(header.Get("Cf-Ray")); v != "" && cloudflare {
			err = err.With(FieldCFRay, v)
		}
		for name, vals := range header {
			lower := strings.ToLower(strings.TrimSpace(name))
			if !strings.HasPrefix(lower, "x-ratelimit-") {
				continue
			}
			if v := strings.TrimSpace(firstHeaderValue(vals)); v != "" {
				err = err.With(FieldHeaderPrefix+lower, v)
			}
		}
	}
	return err, true
}

func firstHeaderValue(vals []string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// decodeWorkersEnvelope decodes a Cloudflare error body; a body that is not one
// decodes to the zero envelope.
func decodeWorkersEnvelope(body []byte) workersErrorEnvelope {
	var env workersErrorEnvelope
	if len(body) == 0 {
		return env
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return workersErrorEnvelope{}
	}
	return env
}

// structuredCode is the first Cloudflare throttle code in env's errors[].code, or a
// hint that is exactly a throttle code; "" when there is none.
func structuredCode(env workersErrorEnvelope, hints []string) string {
	for _, e := range env.Errors {
		if c := parseCFCode(e.Code); c != "" {
			return c
		}
	}
	for _, h := range hints {
		if c := knownCFCode(h); c != "" {
			return c
		}
	}
	return ""
}

func parseCFCode(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return knownCFCode(strconv.Itoa(n))
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return knownCFCode(strings.TrimSpace(s))
	}
	return knownCFCode(strings.Trim(string(raw), `"`))
}

func knownCFCode(code string) string {
	code = strings.TrimSpace(code)
	switch code {
	case WorkersQuotaCode, WorkersCapacityCode, EdgeBlockCode:
		return code
	default:
		return ""
	}
}

func workersCodeFromText(s string) string {
	switch {
	case code3036.FindString(s) != "":
		return WorkersQuotaCode
	case code3040.FindString(s) != "":
		return WorkersCapacityCode
	case code1015.FindString(s) != "":
		return EdgeBlockCode
	default:
		return ""
	}
}

func firstWorkersMessage(env workersErrorEnvelope, hints ...string) string {
	for _, e := range env.Errors {
		if msg := strings.TrimSpace(e.Message); msg != "" {
			return msg
		}
	}
	for _, h := range hints {
		if msg := strings.TrimSpace(h); msg != "" {
			return msg
		}
	}
	return ""
}

func annotateQuota(msg string) string {
	if msg == "" {
		msg = "Cloudflare daily neuron quota is exhausted"
	}
	if !strings.Contains(strings.ToLower(msg), "00:00") {
		msg = strings.TrimSuffix(msg, ".") + ". Quota resets daily at 00:00 UTC."
	}
	return msg
}

func annotateCapacity(msg string) string {
	if msg == "" {
		msg = "Cloudflare model capacity is temporarily exceeded"
	}
	if !strings.Contains(strings.ToLower(msg), "retry") {
		msg = strings.TrimSuffix(msg, ".") + ". Transient capacity; retry with backoff."
	}
	return msg
}

func annotateEdge(msg string) string {
	if msg == "" {
		msg = "Cloudflare edge rate limit (1015)"
	}
	return msg
}

// firstHeader is the first non-empty value among names. Header.Get canonicalises
// each name, so spellings that differ only in case are one lookup.
func firstHeader(h http.Header, names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(h.Get(name)); v != "" {
			return v
		}
	}
	return ""
}

// RetryAfter reads the Retry-After header in h as a wait measured from now. The
// header is either delay-seconds or an HTTP date (RFC 9110 section 10.2.3). ok is
// false when the header is absent or unparsable; a date already past is a zero
// wait.
func RetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	if h == nil {
		return 0, false
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		// Cap before converting so a huge value cannot overflow the Duration.
		return time.Duration(min(secs, maxRetryAfterSeconds)) * time.Second, true
	}
	at, err := http.ParseTime(v)
	if err != nil {
		return 0, false
	}
	return max(at.Sub(now), 0), true
}

// maxRetryAfterSeconds caps a delay-seconds Retry-After at about a year, far past
// any request deadline and well inside time.Duration.
const maxRetryAfterSeconds = 366 * 24 * 60 * 60
