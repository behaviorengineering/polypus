package overlay

import (
	"strings"
)

// File is the on-disk models-allow-overlay.yaml shape.
type File struct {
	Backends map[string]Backend `yaml:"backends"`
}

// Backend lists extra downstream model ids for one backend.
type Backend struct {
	Allow []string `yaml:"allow"`
}

// Removal records one pruned overlay slug.
type Removal struct {
	Backend string
	Model   string
}

// Prune drops overlay slugs that already appear in yamlAllow for the same backend.
func Prune(overlay File, yamlAllow map[string][]string) (out File, removed []Removal) {
	if overlay.Backends == nil {
		return File{}, nil
	}
	outBackends := make(map[string]Backend, len(overlay.Backends))
	for backendID, ob := range overlay.Backends {
		yamlSlugs := yamlAllow[backendID]
		var kept []string
		for _, slug := range ob.Allow {
			if slugInYAML(backendID, slug, yamlSlugs) {
				removed = append(removed, Removal{Backend: backendID, Model: slug})
				continue
			}
			kept = append(kept, slug)
		}
		if len(kept) > 0 {
			outBackends[backendID] = Backend{Allow: kept}
		}
	}
	if len(outBackends) == 0 {
		return File{}, removed
	}
	return File{Backends: outBackends}, removed
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
