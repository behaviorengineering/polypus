package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestCatalogPaginated(t *testing.T) {
	var pages []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ai/models/search") {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, len(pages)+1)
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1", "":
			_, _ = w.Write([]byte(`{
				"success": true,
				"result": [{"name": "@cf/zai-org/glm-4.7-flash"}, {"name": "google/gemma-4-26b-a4b-it"}],
				"result_info": {"page": 1, "per_page": 100, "total_pages": 2, "count": 2}
			}`))
		case "2":
			_, _ = w.Write([]byte(`{
				"success": true,
				"result": [{"name": "@cf/deepgram/aura-2-en"}],
				"result_info": {"page": 2, "per_page": 100, "total_pages": 2, "count": 1}
			}`))
		default:
			http.Error(w, "bad page", http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("CF_TEST_KEY", "secret")
	client, err := NewClient(config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   srv.URL + "/client/v4/accounts/test-acc/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_TEST_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	list := client.ListModels(context.Background())
	if list.Object != "list" {
		t.Fatalf("object: %q", list.Object)
	}
	if len(list.Data) != 3 {
		t.Fatalf("len=%d data=%+v", len(list.Data), list.Data)
	}
	if len(pages) != 2 {
		t.Fatalf("pages fetched: %v", pages)
	}
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ai/models/search") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":[],"result_info":{"page":1,"total_pages":1}}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("CF_TEST_KEY", "secret")
	client, err := NewClient(config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   srv.URL + "/client/v4/accounts/test-acc/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_TEST_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogFailureEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("CF_TEST_KEY", "secret")
	client, err := NewClient(config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   srv.URL + "/client/v4/accounts/test-acc/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_TEST_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	list := client.ListModels(context.Background())
	if list.Object != "list" || len(list.Data) != 0 {
		t.Fatalf("want empty list, got %+v", list)
	}
	_, strictErr := client.ListModelsStrict(context.Background())
	if strictErr == nil {
		t.Fatal("expected strict error with no cache")
	}
}

func TestCatalogFailureUsesStaleCache(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"@cf/cached","task":"text"}],"result_info":{"page":1,"total_pages":1}}`))
			return
		}
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("CF_TEST_KEY", "secret")
	client, err := NewClient(config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   srv.URL + "/client/v4/accounts/test-acc/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_TEST_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.ListModelsStrict(context.Background())
	if err != nil || len(first.Data) != 1 {
		t.Fatalf("first: %+v err=%v", first, err)
	}
	second, err := client.ListModelsStrict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 || second.Data[0].ID != "@cf/cached" {
		t.Fatalf("stale: %+v", second)
	}
}

func TestNormalizeWorkersAIModelName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"@cf/foo/bar", "@cf/foo/bar"},
		{"foo/bar", "@cf/foo/bar"},
		{"cf_local/@cf/zai-org/glm-4.7-flash", "@cf/zai-org/glm-4.7-flash"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeWorkersAIModelName(tc.in); got != tc.want {
			t.Fatalf("normalize(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestSpeechSynthesize(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3-bytes"))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("CF_TEST_KEY", "secret")
	client, err := NewClient(config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   srv.URL + "/client/v4/accounts/test/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_TEST_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}

	audio, _, err := client.Synthesize(context.Background(), SpeechRequest{
		Model: "@cf/deepgram/aura-2-en",
		Input: "Episode one.",
		Voice: "luna",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "mp3-bytes" {
		t.Fatalf("audio: %q", audio)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("auth: %q", gotAuth)
	}
	if !strings.Contains(gotPath, "/run/@cf/deepgram/aura-2-en") {
		t.Fatalf("path: %q", gotPath)
	}
}

func TestSpeechSynthesizeJSONStringResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":"bXAzLWJ5dGVz"}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("CF_TEST_KEY", "secret")
	client, err := NewClient(config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   srv.URL + "/client/v4/accounts/test/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_TEST_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}

	audio, _, err := client.Synthesize(context.Background(), SpeechRequest{
		Model: "@cf/deepgram/aura-2-en",
		Input: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "mp3-bytes" {
		t.Fatalf("audio: %q", audio)
	}
}

func TestSpeechTranscribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "audio/mpeg" {
			t.Fatalf("content-type: %q", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":{"text":"accurate"}}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("CF_TEST_KEY", "secret")
	client, err := NewClient(config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   srv.URL + "/client/v4/accounts/test/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_TEST_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}

	text, err := client.Transcribe(context.Background(), TranscriptionRequest{
		Model:    "@cf/deepgram/nova-3",
		Audio:    []byte("fake-mp3"),
		Filename: "clip.mp3",
		Language: "en-AU",
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "accurate" {
		t.Fatalf("text: %q", text)
	}
}

func TestModelListJSON(t *testing.T) {
	raw, err := json.Marshal(ModelList{
		Object: "list",
		Data:   []Model{{ID: "@cf/a", Object: "model", OwnedBy: "cloudflare"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"object":"list"`) {
		t.Fatalf("%s", raw)
	}
}
