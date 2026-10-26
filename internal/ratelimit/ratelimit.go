// Package ratelimit is the provider-neutral shape of an upstream rate limit. Each client
// classifies its throttled responses into a CodeRateLimited error carrying these
// fields, and the gateway turns the fields back into response headers. Cloudflare is
// the one upstream that also signals throttles by code in the body (3036, 3040, 1015),
// so Classify recognises those codes whichever backend relayed them.
package ratelimit

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

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

// Classify returns a CodeRateLimited error when the status is 429, or the JSON body
// or hints carry a Cloudflare throttle code. It returns nil when the response is not
// a rate limit. op names the caller (and so the backend) that saw the response. Use
// it for Cloudflare and for paths that may relay Cloudflare (Switchyard, proxies).
func Classify(op string, status int, header http.Header, body []byte, hints ...string) *derrors.Error {
	blob := strings.Join(hints, "\n")
	if len(body) > 0 {
		blob += "\n" + string(body)
	}
	cfCode := workersCodeFromEnvelope(body)
	if cfCode == "" {
		cfCode = workersCodeFromText(blob)
	}
	return classify(op, status, header, body, cfCode, hints, true)
}

// ClassifyStatus is Classify for a backend that is not Cloudflare: only a 429 is a
// rate limit, Cloudflare codes in the body mean nothing, and the error carries none
// of the Cloudflare fields (limit_kind, cf_ray) that mark a failure as Cloudflare's.
func ClassifyStatus(op string, status int, header http.Header, body []byte, hints ...string) *derrors.Error {
	return classify(op, status, header, body, "", hints, false)
}

func classify(op string, status int, header http.Header, body []byte, cfCode string, hints []string, cloudflare bool) *derrors.Error {
	if strings.TrimSpace(op) == "" {
		op = "ratelimit.Classify"
	}

	if status != http.StatusTooManyRequests && cfCode == "" {
		return nil
	}
	if status == 0 {
		status = http.StatusTooManyRequests
	}

	msg := firstWorkersMessage(body, hints...)
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
		if v := firstHeader(header, "Ratelimit", "RateLimit", "Rate-Limit"); v != "" {
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
	return err
}

func firstHeaderValue(vals []string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func workersCodeFromEnvelope(body []byte) string {
	var env workersErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	for _, e := range env.Errors {
		if c := parseCFCode(e.Code); c != "" {
			return c
		}
		if c := workersCodeFromText(e.Message); c != "" {
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
	switch strings.TrimSpace(code) {
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

func firstWorkersMessage(body []byte, hints ...string) string {
	var env workersErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil {
		for _, e := range env.Errors {
			if msg := strings.TrimSpace(e.Message); msg != "" {
				return msg
			}
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

func firstHeader(h http.Header, names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(h.Get(name)); v != "" {
			return v
		}
	}
	return ""
}
