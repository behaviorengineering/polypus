package batch

import (
	"strconv"
	"strings"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// ExpiresAtUnix returns createdAt plus the OpenAI completion_window duration (e.g. "24h").
func ExpiresAtUnix(createdAt int64, completionWindow string) (int64, error) {
	d, err := ParseCompletionWindow(completionWindow)
	if err != nil {
		return 0, err
	}
	return createdAt + int64(d.Seconds()), nil
}

// ParseCompletionWindow parses OpenAI batch completion_window strings.
func ParseCompletionWindow(window string) (time.Duration, error) {
	window = strings.TrimSpace(strings.ToLower(window))
	if window == "" {
		return 24 * time.Hour, nil
	}
	if strings.HasSuffix(window, "h") {
		n, err := strconv.Atoi(strings.TrimSuffix(window, "h"))
		if err != nil || n <= 0 {
			return 0, derrors.New(derrors.CodeInvalid, "batch.ParseCompletionWindow", "invalid completion_window").
				With("completion_window", window)
		}
		return time.Duration(n) * time.Hour, nil
	}
	if strings.HasSuffix(window, "m") {
		n, err := strconv.Atoi(strings.TrimSuffix(window, "m"))
		if err != nil || n <= 0 {
			return 0, derrors.New(derrors.CodeInvalid, "batch.ParseCompletionWindow", "invalid completion_window").
				With("completion_window", window)
		}
		return time.Duration(n) * time.Minute, nil
	}
	return 0, derrors.New(derrors.CodeInvalid, "batch.ParseCompletionWindow", "unsupported completion_window").
		With("completion_window", window)
}
