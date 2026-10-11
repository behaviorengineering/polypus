package gateway

import (
	"errors"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

// isBatchRefreshMiss reports whether a Cloudflare poll failure should return last-known batch metadata.
func isBatchRefreshMiss(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, derrors.ErrUnavailable) ||
		errors.Is(err, derrors.ErrTimeout) ||
		errors.Is(err, derrors.ErrRateLimited) {
		return true
	}
	return upstream.Unavailable(err)
}
