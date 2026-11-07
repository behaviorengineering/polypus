package overlay

import (
	"testing"
)

func yamlAllowGraniteOnly() map[string][]string {
	return map[string][]string{
		"deepinfra": {"ibm-granite/granite-4.2-8b"},
	}
}

func windowsOverlayDump() file {
	return file{
		Backends: map[string]backend{
			"deepinfra":  {Allow: []string{"ibm-granite/granite-4.2-8b"}},
			"openrouter": {Allow: []string{"meta-llama/llama-3.3-70b-instruct"}},
		},
	}
}

func TestPruneRemovesGitDuplicateKeepsOverlayOnly(t *testing.T) {
	out, removed := prune(windowsOverlayDump(), yamlAllowGraniteOnly())
	if len(removed) != 1 || removed[0].Backend != "deepinfra" {
		t.Fatalf("removed %v", removed)
	}
	if len(out.Backends) != 1 {
		t.Fatalf("backends %v", out.Backends)
	}
	or := out.Backends["openrouter"].Allow
	if len(or) != 1 || or[0] != "meta-llama/llama-3.3-70b-instruct" {
		t.Fatalf("openrouter allow %v", or)
	}
}

func TestPruneRemovesBothWhenYamlHasLlama(t *testing.T) {
	yaml := yamlAllowGraniteOnly()
	yaml["openrouter"] = []string{"meta-llama/llama-3.3-70b-instruct"}
	out, removed := prune(windowsOverlayDump(), yaml)
	if len(removed) != 2 {
		t.Fatalf("removed %v", removed)
	}
	if len(out.Backends) > 0 {
		t.Fatalf("expected empty out %v", out.Backends)
	}
}

func TestPruneEmptyOverlay(t *testing.T) {
	out, removed := prune(file{}, yamlAllowGraniteOnly())
	if len(removed) != 0 {
		t.Fatalf("removed %v", removed)
	}
	if len(out.Backends) > 0 {
		t.Fatalf("backends %v", out.Backends)
	}
}

func TestPruneKeepsUnknownBackend(t *testing.T) {
	overlay := file{
		Backends: map[string]backend{
			"stale_backend": {Allow: []string{"some/model"}},
		},
	}
	out, removed := prune(overlay, yamlAllowGraniteOnly())
	if len(removed) != 0 {
		t.Fatalf("removed %v", removed)
	}
	if out.Backends["stale_backend"].Allow[0] != "some/model" {
		t.Fatalf("backends %v", out.Backends)
	}
}

func TestCanonicalPreservesCase(t *testing.T) {
	yaml := map[string][]string{
		"deepinfra": {"google/gemma-3-27b-it"},
	}
	overlay := file{
		Backends: map[string]backend{
			"deepinfra": {Allow: []string{"google/gemma-3-27B-it"}},
		},
	}
	out, removed := prune(overlay, yaml)
	if len(removed) != 0 {
		t.Fatalf("case mismatch must not prune: removed %v out %v", removed, out.Backends)
	}
}
