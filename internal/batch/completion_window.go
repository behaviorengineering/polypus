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

// ParseCompletionWindow parses OpenAI batch completion_window strings: a positive
// whole number of hours or minutes ("24h", "90m"). Empty means 24h.
func ParseCompletionWindow(window string) (time.Duration, error) {
	const op = "batch.ParseCompletionWindow"
	window = strings.TrimSpace(strings.ToLower(window))
	if window == "" {
		return 24 * time.Hour, nil
	}
	if !strings.HasSuffix(window, "h") && !strings.HasSuffix(window, "m") {
		return 0, derrors.New(derrors.CodeInvalid, op, "unsupported completion_window").With("completion_window", window)
	}
	// One unit only, as a whole number: "1h30m" and "1.5h" are refused.
	if _, err := strconv.Atoi(window[:len(window)-1]); err != nil {
		return 0, derrors.New(derrors.CodeInvalid, op, "invalid completion_window").With("completion_window", window)
	}
	d, err := time.ParseDuration(window)
	if err != nil || d <= 0 {
		return 0, derrors.New(derrors.CodeInvalid, op, "invalid completion_window").With("completion_window", window)
	}
	return d, nil
}
