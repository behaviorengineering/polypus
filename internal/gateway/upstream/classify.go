package upstream

import (
	"errors"
	"strconv"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/sony/gobreaker"
)

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

// ClassifyDialFailure maps a dial error and upstream name to a failure class.
func ClassifyDialFailure(err error, upstreamName string) FailureClass {
	if err == nil {
		return FailureClass{}
	}
	upstreamName = normalizeName(upstreamName)
	if Unavailable(err) {
		state := "open"
		if errors.Is(err, gobreaker.ErrTooManyRequests) {
			state = "half-open"
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
	status := bifrostStatus(err)
	if upstreamName == "cf_local" || status > 0 {
		return FailureClass{
			Layer:      LayerCloudflare,
			Upstream:   upstreamName,
			HTTPStatus: status,
		}
	}
	return FailureClass{
		Layer:    LayerLeaf,
		Upstream: upstreamName,
	}
}

func bifrostStatus(err error) int {
	var de *derrors.Error
	for e := err; e != nil; {
		if errors.As(e, &de) && de != nil {
			if fields := de.Fields(); fields != nil {
				if raw, ok := fields["status"]; ok {
					if n, convErr := strconv.Atoi(strings.TrimSpace(raw)); convErr == nil {
						return n
					}
				}
			}
		}
		e = errors.Unwrap(e)
	}
	msg := strings.ToLower(err.Error())
	if idx := strings.Index(msg, "status "); idx >= 0 {
		rest := strings.TrimSpace(msg[idx+len("status "):])
		if n, convErr := strconv.Atoi(strings.Fields(rest)[0]); convErr == nil {
			return n
		}
	}
	return 0
}
