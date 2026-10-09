package otelcol

import (
	"fmt"
	"strings"
)

// normalizeTraceIDHex returns a 32-char lowercase hex trace id for Phoenix / HyperDX queries.
func normalizeTraceIDHex(traceID string) (string, error) {
	id := strings.TrimSpace(strings.ToLower(traceID))
	id = strings.TrimPrefix(id, "0x")
	if len(id) != 32 {
		return "", fmt.Errorf("collector smoke: trace id length %d, want 32 hex chars", len(id))
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("collector smoke: trace id invalid hex at index %d", i)
		}
	}
	return id, nil
}
