package observability

import "sync/atomic"

var failureDumpDir atomic.Value // string

// SetFailureDumpDir records the active dump directory for debug fetch handlers.
func SetFailureDumpDir(dir string) {
	failureDumpDir.Store(dir)
}

// FailureDumpDir returns the configured dump directory (empty when dumps disabled).
func FailureDumpDir() string {
	v, ok := failureDumpDir.Load().(string)
	if !ok {
		return ""
	}
	return v
}
