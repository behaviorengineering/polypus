package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestAnthropicListModelsHeadersAndShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" || r.Header.Get("anthropic-version") != apiVersion {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("limit") == "" {
			http.Error(w, "limit missing", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"type":"model","id":"claude-sonnet-5-5","display_name":"Claude Sonnet 5.5","created_at":"2026-05-01T00:00:00Z"}],"has_more":false,"last_id":"claude-sonnet-5-5"}`))
	}))
	t.Cleanup(srv.Close)

	client, err := newClient(srv.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := client.ListModelsStrict(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != "claude-sonnet-5-5" || list.Data[0].OwnedBy != "anthropic" {
		t.Fatalf("got %#v", list.Data)
	}
	if want := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix(); list.Data[0].Created != want {
		t.Fatalf("created: got %d want %d", list.Data[0].Created, want)
	}
}

func TestAnthropicListModelsFollowsPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-a"}],"has_more":true,"last_id":"claude-a"}`))
			return
		}
		if r.URL.Query().Get("after_id") != "claude-a" {
			http.Error(w, "bad cursor", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-b"}],"has_more":false,"last_id":"claude-b"}`))
	}))
	t.Cleanup(srv.Close)

	client, err := newClient(srv.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := client.ListModelsStrict(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 2 || list.Data[0].ID != "claude-a" || list.Data[1].ID != "claude-b" {
		t.Fatalf("got %#v", list.Data)
	}
}

func TestAnthropicListModelsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"type":"error","error":{"type":"authentication_error"}}`, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	client, err := newClient(srv.URL, "bad")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = client.ListModelsStrict(ctx)
	var de *derrors.Error
	if !errors.As(err, &de) || de.Code() != derrors.CodeUnauthorized {
		t.Fatalf("err: %v", err)
	}
}

func TestModelsListURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                                    defaultModelsURL,
		"https://api.anthropic.com":           "https://api.anthropic.com/v1/models",
		"https://api.anthropic.com/":          "https://api.anthropic.com/v1/models",
		"https://api.anthropic.com/v1":        "https://api.anthropic.com/v1/models",
		"https://api.anthropic.com/v1/models": "https://api.anthropic.com/v1/models",
	} {
		if got := modelsListURL(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestNewClientRejectsOtherExtensions(t *testing.T) {
	_, err := newClientFromBackend(config.BackendDef{ID: "g", Extension: config.ExtensionGemini})
	if !errors.Is(err, errNotAnthropicExtension) {
		t.Fatalf("err: %v", err)
	}
}
