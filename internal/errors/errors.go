// Package errors defines typed domain errors for Polypus.
//
// Each layer MUST wrap the incoming cause with its own *Error (code, op, message,
// optional fields) and return it. Go has no Java-style stack on error values;
// the Unwrap chain plus fields is the breadcrumb for humans and logs.
//
// Import as derrors when the file also needs the standard library errors package.
package errors

import (
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
	// CodeInternal is an unexpected local failure.
	CodeInternal Code = "internal"
	// CodeConflict is a version or state clash.
	CodeConflict Code = "conflict"
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
	ErrInternal           = &Error{code: CodeInternal, message: "internal"}
	ErrConflict           = &Error{code: CodeConflict, message: "conflict"}
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

// NewDomainError is the pack-compatible constructor: code, message, optional cause.
func NewDomainError(code Code, message string, cause error) *Error {
	if cause == nil {
		return New(code, "", message)
	}
	return Wrap(cause, code, "", message)
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

// CodeOf returns the outermost domain Code, or CodeInternal if err is not *Error.
func CodeOf(err error) Code {
	for err != nil {
		if e, ok := err.(*Error); ok {
			return e.code
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return CodeInternal
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
	case CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusBadGateway
	}
}
