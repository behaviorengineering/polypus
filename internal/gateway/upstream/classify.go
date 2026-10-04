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

// ResolveUpstreamName returns hint when set, else the name from mapExecuteErr wraps.
func ResolveUpstreamName(err error, hint string) string {
	hint = strings.TrimSpace(hint)
	if hint != "" && hint != "unknown" {
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
	if isCloudflareDialFailure(err) {
		return FailureClass{
			Layer:      LayerCloudflare,
			Upstream:   upstreamName,
			HTTPStatus: status,
		}
	}
	if status > 0 {
		return FailureClass{
			Layer:      LayerLeaf,
			Upstream:   upstreamName,
			HTTPStatus: status,
		}
	}
	return FailureClass{
		Layer:    LayerLeaf,
		Upstream: upstreamName,
	}
}

func isCloudflareDialFailure(err error) bool {
	return hasCloudflareMetadata(err) || hasCloudflareOp(err)
}

func hasCloudflareMetadata(err error) bool {
	for _, key := range []string{"cf_code", "cf_ray", "limit_kind"} {
		if derrors.Field(err, key) != "" {
			return true
		}
	}
	return false
}

func hasCloudflareOp(err error) bool {
	var de *derrors.Error
	for e := err; e != nil; {
		if errors.As(e, &de) && de != nil {
			if strings.HasPrefix(de.Op(), "cloudflare.") {
				return true
			}
		}
		e = errors.Unwrap(e)
	}
	return false
}

func upstreamNameFromUnavailable(err error) string {
	const suffix = " unavailable:"
	for e := err; e != nil; {
		msg := e.Error()
		const prefix = "upstream "
		if i := strings.Index(msg, prefix); i >= 0 {
			rest := msg[i+len(prefix):]
			if j := strings.Index(rest, suffix); j > 0 {
				return normalizeName(strings.TrimSpace(rest[:j]))
			}
		}
		e = errors.Unwrap(e)
	}
	return ""
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
