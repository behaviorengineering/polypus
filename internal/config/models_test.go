package config

import "testing"

func TestModelInAllowList(t *testing.T) {
	allow := []string{"@cf/zai-org/glm-4.7-flash", "allenai/olmocr-2-7b"}
	cases := []struct {
		backend, model string
		want           bool
	}{
		{"cf_local", "cf_local/@cf/zai-org/glm-4.7-flash", true},
		{"cf_local", "@cf/zai-org/glm-4.7-flash", true},
		{"cf_local", "@cf/other", false},
		{"lm_studio", "lm_studio/allenai/olmocr-2-7b", true},
		{"lm_studio", "allenai/olmocr-2-7b", true},
		{"lm_studio", "other", false},
	}
	for _, tc := range cases {
		if got := ModelInAllowList(tc.backend, tc.model, allow); got != tc.want {
			t.Fatalf("%s %s: got %v want %v", tc.backend, tc.model, got, tc.want)
		}
	}
}

func TestIsModelAllowedNoGate(t *testing.T) {
	b := BackendDef{ID: "lm_studio"}
	if !b.IsModelAllowed("anything") {
		t.Fatal("expected open when no models block")
	}
}

func TestIsModelAllowedEmptyList(t *testing.T) {
	b := BackendDef{
		ID: "cf_local",
		Models: &BackendModels{
			AllowConfigured: true,
			Allow:           nil,
		},
	}
	if b.IsModelAllowed("@cf/foo") {
		t.Fatal("empty allow should reject")
	}
}

func TestSyntheticAllowModels(t *testing.T) {
	ids := SyntheticAllowModels("cf_local", []string{
		"@cf/a",
		"cf_local/@cf/b",
		"@cf/a",
	})
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen["cf_local/@cf/a"] || !seen["cf_local/@cf/b"] || len(seen) != 2 {
		t.Fatalf("%v", ids)
	}
}

func TestShouldSyncDefault(t *testing.T) {
	var m *BackendModels
	if !m.ShouldSync() {
		t.Fatal("nil ShouldSync true")
	}
	f := false
	m = &BackendModels{Sync: &f}
	if m.ShouldSync() {
		t.Fatal("sync false")
	}
}
