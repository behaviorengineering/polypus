// Package errors defines typed domain errors for Polypus.
//
// Each layer MUST wrap the incoming cause with its own *Error (code, op, message,
// optional fields) and return it. Go has no Java-style stack on error values;
// the Unwrap chain plus fields is the breadcrumb for humans and logs.
//
// Import as derrors when the file also needs the standard library errors package.
package errors

import (
	stderrors "errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
)

// Code is a stable machine-readable class for logs, metrics, and errors.Is.
type Code string

const (
	// CodeInvalid is a bad caller argument.
	CodeInvalid Code = "invalid"
	// CodeNotFound is a missing backend, model, or resource.
	CodeNotFound Code = "not_found"
	// CodeUnauthorized is missing or rejected credentials.
	CodeUnauthorized Code = "unauthorized"
	// CodeTimeout is a deadline or idle timeout.
	CodeTimeout Code = "timeout"
	// CodeCanceled is a caller cancel.
	CodeCanceled Code = "canceled"
	// CodeFailedPrecondition is a required bound or state missing (for example no deadline).
	CodeFailedPrecondition Code = "failed_precondition"
	// CodeUnavailable is a downstream hop that failed or is unreachable.
	CodeUnavailable Code = "unavailable"
	// CodeRateLimited is an upstream throttle (HTTP 429), including quota and capacity.
	CodeRateLimited Code = "rate_limited"
	// CodeNotReady is local config or dependency not ready to serve (HTTP 503).
	CodeNotReady Code = "not_ready"
	// CodeInternal is an unexpected local failure.
	CodeInternal Code = "internal"
	// CodeConflict is a version or state clash.
	CodeConflict Code = "conflict"
	// CodeUnimplemented is a valid request the server does not support yet (for example upstream has no cancel).
	CodeUnimplemented Code = "unimplemented"
)

// StatusClientClosedRequest is nginx 499 (client closed the request).
const StatusClientClosedRequest = 499

// Sentinel values for errors.Is. Match by Code only.
var (
	ErrInvalid            = &Error{code: CodeInvalid, message: "invalid argument"}
	ErrNotFound           = &Error{code: CodeNotFound, message: "not found"}
	ErrUnauthorized       = &Error{code: CodeUnauthorized, message: "unauthorized"}
	ErrTimeout            = &Error{code: CodeTimeout, message: "timeout"}
	ErrCanceled           = &Error{code: CodeCanceled, message: "canceled"}
	ErrFailedPrecondition = &Error{code: CodeFailedPrecondition, message: "failed precondition"}
	ErrUnavailable        = &Error{code: CodeUnavailable, message: "unavailable"}
	ErrRateLimited        = &Error{code: CodeRateLimited, message: "rate limited"}
	ErrNotReady           = &Error{code: CodeNotReady, message: "not ready"}
	ErrInternal           = &Error{code: CodeInternal, message: "internal"}
	ErrConflict           = &Error{code: CodeConflict, message: "conflict"}
	ErrUnimplemented      = &Error{code: CodeUnimplemented, message: "not implemented"}
)

// Error is one hop in a wrap chain.
type Error struct {
	code    Code
	op      string
	message string
	fields  map[string]string
	cause   error
}

// New returns a layer error with no cause.
func New(code Code, op, message string) *Error {
	if code == "" {
		code = CodeInternal
	}
	return &Error{code: code, op: op, message: message}
}

// Wrap returns a layer error that unwraps to cause.
// A nil cause is equivalent to New.
func Wrap(cause error, code Code, op, message string) *Error {
	if cause == nil {
		return New(code, op, message)
	}
	if code == "" {
		code = CodeInternal
	}
	return &Error{code: code, op: op, message: message, cause: cause}
}

// Code returns the machine-readable class.
func (e *Error) Code() Code {
	if e == nil {
		return ""
	}
	return e.code
}

// Op returns the layer operation name (for example "router.Synthesize").
func (e *Error) Op() string {
	if e == nil {
		return ""
	}
	return e.op
}

// Message returns the hop message without the cause suffix.
func (e *Error) Message() string {
	if e == nil {
		return ""
	}
	return e.message
}

// Fields returns a copy of troubleshooting metadata.
func (e *Error) Fields() map[string]string {
	if e == nil || len(e.fields) == 0 {
		return nil
	}
	return maps.Clone(e.fields)
}

// With returns a copy with one metadata field set. It does not mutate e.
func (e *Error) With(key, value string) *Error {
	if e == nil {
		return nil
	}
	out := *e
	out.fields = maps.Clone(e.fields)
	if out.fields == nil {
		out.fields = make(map[string]string, 1)
	}
	out.fields[key] = value
	return &out
}

// Error implements error. The string includes op, message, and cause for logs.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	if e.op != "" {
		b.WriteString(e.op)
		b.WriteString(": ")
	}
	if e.message != "" {
		b.WriteString(e.message)
	} else {
		b.WriteString(string(e.code))
	}
	if e.cause != nil {
		b.WriteString(": ")
		b.WriteString(e.cause.Error())
	}
	return b.String()
}

// Unwrap returns the wrapped cause.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is reports whether target is an *Error with the same Code.
func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	t, ok := target.(*Error)
	if !ok || t == nil {
		return false
	}
	return e.code == t.code
}

// Format prints code, op, fields, and the cause chain when the verb is %+v.
func (e *Error) Format(f fmt.State, verb rune) {
	if e == nil {
		return
	}
	if verb == 'v' && f.Flag('+') {
		_, _ = fmt.Fprintf(f, "code=%s op=%s message=%s", e.code, e.op, e.message)
		if len(e.fields) > 0 {
			_, _ = fmt.Fprintf(f, " fields=%v", e.fields)
		}
		if e.cause != nil {
			_, _ = fmt.Fprint(f, "\n  caused by: ")
			if formatter, ok := e.cause.(fmt.Formatter); ok {
				formatter.Format(f, verb)
			} else {
				_, _ = fmt.Fprint(f, e.cause.Error())
			}
		}
		return
	}
	_, _ = io.WriteString(f, e.Error())
}

// CodeOr returns the outermost domain Code in err's chain, or fallback when the
// chain holds no *Error. Wraps use it to keep an inner rate-limit or invalid-input
// code instead of masking it with their own.
func CodeOr(err error, fallback Code) Code {
	var de *Error
	if stderrors.As(err, &de) && de != nil {
		return de.code
	}
	return fallback
}

// CodeOf returns the outermost domain Code, or CodeInternal if err holds no *Error.
// errors.As also follows multi-%w wraps such as fmt.Errorf("%w: %w", de, sentinel).
func CodeOf(err error) Code {
	var de *Error
	if stderrors.As(err, &de) && de != nil {
		return de.code
	}
	return CodeInternal
}

// PublicMessage is the error text safe to return to an HTTP client: the domain
// messages along the chain, without op names. Foreign causes (dial errors, URLs,
// parser output) are kept only for caller errors (4xx), where they describe the
// request; for 5xx they stay in the server log.
func PublicMessage(err error) string {
	if err == nil {
		return ""
	}
	callerError := HTTPStatus(err) < http.StatusInternalServerError
	var parts []string
	for e := err; e != nil; {
		var de *Error
		if !stderrors.As(e, &de) || de == nil {
			if callerError {
				parts = append(parts, strings.TrimSpace(e.Error()))
			}
			break
		}
		if m := strings.TrimSpace(de.message); m != "" {
			parts = append(parts, m)
		}
		e = de.cause
	}
	if len(parts) == 0 {
		return http.StatusText(HTTPStatus(err))
	}
	return strings.Join(parts, ": ")
}

// HTTPStatus maps a domain code to an HTTP status for gateway handlers.
func HTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	switch CodeOf(err) {
	case CodeInvalid, CodeFailedPrecondition:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeTimeout:
		return http.StatusGatewayTimeout
	case CodeCanceled:
		return StatusClientClosedRequest
	case CodeNotReady:
		return http.StatusServiceUnavailable
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeUnimplemented:
		return http.StatusNotImplemented
	case CodeInternal:
		return http.StatusInternalServerError
	case CodeUnavailable:
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}

// Field returns the first metadata value for key, outermost first, anywhere in err's
// tree.
func Field(err error, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	var out string
	Walk(err, func(de *Error) bool {
		out = strings.TrimSpace(de.fields[key])
		return out == ""
	})
	return out
}

// Walk calls fn for every *Error in err's tree, outermost first and depth first,
// following both Unwrap() error and the Unwrap() []error of joined errors. It stops
// when fn returns false.
func Walk(err error, fn func(*Error) bool) {
	walk(err, fn)
}

func walk(err error, fn func(*Error) bool) bool {
	if err == nil {
		return true
	}
	if de, ok := err.(*Error); ok && de != nil { //nolint:errorlint // Walk visits each node itself.
		if !fn(de) {
			return false
		}
	}
	switch u := err.(type) { //nolint:errorlint // Walk follows each unwrap itself.
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if !walk(e, fn) {
				return false
			}
		}
	case interface{ Unwrap() error }:
		return walk(u.Unwrap(), fn)
	}
	return true
}
