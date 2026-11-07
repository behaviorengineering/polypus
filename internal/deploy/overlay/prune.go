package overlay

import (
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
)

// file is the gateway's own overlay type, so both readers agree on its shape.
type file = config.AllowOverlayFile

// backend lists extra downstream model ids for one backend.
type backend = config.AllowOverlayBackend

// removal records one pruned overlay slug.
type removal struct {
	Backend string
	Model   string
}

// prune drops overlay slugs that already appear in yamlAllow for the same backend.
func prune(overlay file, yamlAllow map[string][]string) (out file, removed []removal) {
	if overlay.Backends == nil {
		return file{}, nil
	}
	outBackends := make(map[string]backend, len(overlay.Backends))
	for backendID, ob := range overlay.Backends {
		yamlSlugs := yamlAllow[backendID]
		var kept []string
		for _, slug := range ob.Allow {
			if slugInYAML(backendID, slug, yamlSlugs) {
				removed = append(removed, removal{Backend: backendID, Model: slug})
				continue
			}
			kept = append(kept, slug)
		}
		if len(kept) > 0 {
			outBackends[backendID] = backend{Allow: kept}
		}
	}
	if len(outBackends) == 0 {
		return file{}, removed
	}
	return file{Backends: outBackends}, removed
}

func slugInYAML(backendID, overlaySlug string, yamlSlugs []string) bool {
	oc := canonical(backendID, overlaySlug)
	for _, ys := range yamlSlugs {
		if canonical(backendID, ys) == oc {
			return true
		}
	}
	return false
}

func canonical(backendID, slug string) string {
	s := strings.TrimSpace(slug)
	prefix := backendID + "/"
	if strings.HasPrefix(s, prefix) {
		return strings.TrimSpace(s[len(prefix):])
	}
	return s
}
