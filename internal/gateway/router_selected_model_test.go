package gateway

import (
	"net/http"
	"testing"
)

func TestExtractRouterSelectedModelHeaderWins(t *testing.T) {
	hdr := http.Header{}
	hdr.Set(headerRouterSelectedModel, "cf_local/@cf/a")
	body := []byte(`{"model":"cf_local/@cf/b"}`)
	if got := extractRouterSelectedModel(hdr, body); got != "cf_local/@cf/a" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractRouterSelectedModelFromBody(t *testing.T) {
	body := []byte(`{"model":"lm_studio/qwen","choices":[]}`)
	if got := extractRouterSelectedModel(http.Header{}, body); got != "lm_studio/qwen" {
		t.Fatalf("got %q", got)
	}
}
