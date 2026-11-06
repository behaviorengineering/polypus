package upstream

import (
	"errors"
	"strconv"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
	"github.com/sony/gobreaker"
)

// Layer values attribute a dial failure to the hop that failed.
const (
	LayerCloudflare     = "cloudflare"
	LayerPolypusBreaker = "polypus_breaker"
	LayerSwitchyard     = "switchyard"
	LayerLeaf           = "leaf"
	LayerGateway        = "gateway"
)

// FailureClass is structured attribution for dial failures.
type FailureClass struct {
	Layer        string
	Upstream     string
	HTTPStatus   int
	BreakerState string
}

// ResolveUpstreamName returns hint when set, else the name from mapExecuteErr wraps.
func ResolveUpstreamName(err error, hint string) string {
	hint = strings.TrimSpace(hint)
	if hint != "" && hint != unknownUpstream {
		return normalizeName(hint)
	}
	if name := upstreamNameFromUnavailable(err); name != "" {
		return name
	}
	return normalizeName(hint)
}

// ClassifyDialFailure maps a dial error and upstream name to a failure class.
func ClassifyDialFailure(err error, upstreamName string) FailureClass {
	if err == nil {
		return FailureClass{}
	}
	upstreamName = ResolveUpstreamName(err, upstreamName)
	if Unavailable(err) {
		state := stateOpen
		if errors.Is(err, gobreaker.ErrTooManyRequests) {
			state = stateHalfOpen
		}
		return FailureClass{
			Layer:        LayerPolypusBreaker,
			Upstream:     upstreamName,
			BreakerState: state,
		}
	}
	if upstreamName == NameSwitchyard {
		return FailureClass{
			Layer:    LayerSwitchyard,
			Upstream: upstreamName,
		}
	}
	layer := LayerLeaf
	if isCloudflareDialFailure(err) {
		layer = LayerCloudflare
	}
	// HTTPStatus stays 0 when the chain records no upstream status.
	return FailureClass{
		Layer:      layer,
		Upstream:   upstreamName,
		HTTPStatus: upstreamStatus(err),
	}
}

func isCloudflareDialFailure(err error) bool {
	return hasCloudflareMetadata(err) || hasCloudflareOp(err)
}

func hasCloudflareMetadata(err error) bool {
	for _, key := range []string{ratelimit.FieldCFCode, ratelimit.FieldCFRay, ratelimit.FieldKind} {
		if derrors.Field(err, key) != "" {
			return true
		}
	}
	return false
}

func hasCloudflareOp(err error) bool {
	found := false
	derrors.Walk(err, func(de *derrors.Error) bool {
		found = strings.HasPrefix(de.Op(), "cloudflare.")
		return !found
	})
	return found
}

// upstreamNameFromUnavailable is the upstream a breaker refusal names, if any.
func upstreamNameFromUnavailable(err error) string {
	return strings.TrimSpace(derrors.Field(err, FieldUpstream))
}

// upstreamStatus is the upstream HTTP status the error chain records, or 0. Every
// client that dials an upstream sets it (Bifrost, Cloudflare, ratelimit, the router).
func upstreamStatus(err error) int {
	n, convErr := strconv.Atoi(strings.TrimSpace(derrors.Field(err, ratelimit.FieldStatus)))
	if convErr != nil {
		return 0
	}
	return n
}
