//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/media"
)

func TestGatewayLanding(t *testing.T) {
	h, err := Shared()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type: %q", ct)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if !strings.Contains(html, "/health") || !strings.Contains(html, "/media/banner.webp") {
		t.Fatalf("body missing links:\n%s", html)
	}

	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+"/media/banner.webp", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("banner status: %d", resp2.StatusCode)
	}
	if resp2.Header.Get("Content-Type") != "image/webp" {
		t.Fatalf("banner content-type: %q", resp2.Header.Get("Content-Type"))
	}
	banner, err := io.ReadAll(io.LimitReader(resp2.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if len(banner) != len(media.BannerWebP) {
		t.Fatalf("banner len: got %d want %d", len(banner), len(media.BannerWebP))
	}
}
