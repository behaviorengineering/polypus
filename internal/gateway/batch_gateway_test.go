package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestBatchFacadeLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	batchDir := filepath.Join(home, "state", "polypus", "batch")
	t.Setenv("POLYPUS_BATCH_DIR", batchDir)

	var cfCalls int
	cfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfCalls++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "request_id") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"responses": []map[string]interface{}{
						{
							"external_reference": "job-1",
							"success":            true,
							"result":             map[string]string{"response": "hello"},
						},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": map[string]interface{}{
				"status":     "queued",
				"request_id": "cf-1",
			},
		})
	}))
	defer cfSrv.Close()

	dir := t.TempDir()
	yaml := `
batch_backend:
  enabled: true
  default: cf_local
chat_backend:
  enabled: true
  default: cf_local
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
proxy_backend:
  enabled: true
  default: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  cf_local:
    remote: true
    extension: cloudflare
    base_url: ` + cfSrv.URL + `/accounts/test/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, batch]
    models:
      allow:
        - "@cf/meta/llama-3.3-70b-instruct-fp8-fast"
        - "cf_local/@cf/meta/llama-3.3-70b-instruct-fp8-fast"
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("CF_AI_API_KEY", "test-token")

	rcfg, err := config.LoadRouterConfig(config.ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := router.NewClient(rcfg, router.WithCloudflareClientGet(func(b config.BackendDef) (*cloudflare.Client, error) {
		return cloudflare.NewClient(b)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	h, err := NewHandler(config.ServeOptions{}, WithRouter(client))
	if err != nil {
		t.Fatal(err)
	}

	line := `{"custom_id":"job-1","method":"POST","url":"/v1/chat/completions","body":{"model":"cf_local/@cf/meta/llama-3.3-70b-instruct-fp8-fast","messages":[{"role":"user","content":"hi"}]}}`
	fileID := uploadBatchFile(t, h, line)

	createBody := `{"input_file_id":"` + fileID + `","endpoint":"/v1/chat/completions","completion_window":"24h"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/batches", strings.NewReader(createBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create batch: %d %s", rec.Code, rec.Body.String())
	}
	var batchObj batch.BatchMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &batchObj); err != nil {
		t.Fatal(err)
	}
	if batchObj.Status != batch.BatchStatusInProgress {
		t.Fatalf("status: %s", batchObj.Status)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/batches/"+batchObj.ID, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get batch: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &batchObj); err != nil {
		t.Fatal(err)
	}
	if batchObj.Status != batch.BatchStatusCompleted || batchObj.OutputFileID == "" {
		t.Fatalf("final batch: %+v", batchObj)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/files/"+batchObj.OutputFileID+"/content", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("output content: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "job-1") {
		t.Fatalf("output: %s", rec.Body.String())
	}
	if cfCalls < 2 {
		t.Fatalf("expected cf calls, got %d", cfCalls)
	}
}

func uploadBatchFile(t *testing.T, h http.Handler, line string) string {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("purpose", "batch")
	fw, err := w.CreateFormFile("file", "in.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(line + "\n"))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/files", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var file batch.FileRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &file); err != nil {
		t.Fatal(err)
	}
	return file.ID
}

func TestBatchCancelNotSupported(t *testing.T) {
	h := newBatchGatewayHandler(t, nil)
	line := `{"custom_id":"job-1","method":"POST","url":"/v1/chat/completions","body":{"model":"cf_local/@cf/meta/llama-3.3-70b-instruct-fp8-fast","messages":[{"role":"user","content":"hi"}]}}`
	fileID := uploadBatchFile(t, h, line)
	createBody := `{"input_file_id":"` + fileID + `","endpoint":"/v1/chat/completions","completion_window":"24h"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/batches", strings.NewReader(createBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d", rec.Code)
	}
	var batchObj batch.BatchMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &batchObj); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/batches/"+batchObj.ID+"/cancel", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("cancel status: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := h.batchStore.GetBatch(batchObj.ID)
	if got.Status != batch.BatchStatusInProgress {
		t.Fatalf("status unchanged: %s", got.Status)
	}
}

func TestBatchFailedLineErrorMessage(t *testing.T) {
	cf := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "request_id") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"responses": []map[string]interface{}{
						{
							"external_reference": "job-1",
							"success":            false,
							"error":              map[string]string{"message": "cf-line-fail"},
						},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": map[string]interface{}{
				"status":     "queued",
				"request_id": "cf-1",
			},
		})
	}
	h := newBatchGatewayHandler(t, cf)
	line := `{"custom_id":"job-1","method":"POST","url":"/v1/chat/completions","body":{"model":"cf_local/@cf/meta/llama-3.3-70b-instruct-fp8-fast","messages":[{"role":"user","content":"hi"}]}}`
	fileID := uploadBatchFile(t, h, line)
	createBody := `{"input_file_id":"` + fileID + `","endpoint":"/v1/chat/completions","completion_window":"24h"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/batches", strings.NewReader(createBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var batchObj batch.BatchMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &batchObj); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/batches/"+batchObj.ID, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &batchObj); err != nil {
		t.Fatal(err)
	}
	if batchObj.Status != batch.BatchStatusFailed {
		t.Fatalf("status: %s", batchObj.Status)
	}
	if batchObj.ErrorFileID == "" {
		t.Fatal("expected error file")
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/files/"+batchObj.ErrorFileID+"/content", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "cf-line-fail") {
		t.Fatalf("error jsonl: %s", rec.Body.String())
	}
}

func TestBatchConcurrentFinalize(t *testing.T) {
	cf := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "request_id") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"responses": []map[string]interface{}{
						{
							"external_reference": "job-1",
							"success":            true,
							"result":             map[string]string{"response": "hello"},
						},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": map[string]interface{}{
				"status":     "queued",
				"request_id": "cf-1",
			},
		})
	}
	h := newBatchGatewayHandler(t, cf)
	line := `{"custom_id":"job-1","method":"POST","url":"/v1/chat/completions","body":{"model":"cf_local/@cf/meta/llama-3.3-70b-instruct-fp8-fast","messages":[{"role":"user","content":"hi"}]}}`
	fileID := uploadBatchFile(t, h, line)
	createBody := `{"input_file_id":"` + fileID + `","endpoint":"/v1/chat/completions","completion_window":"24h"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/batches", strings.NewReader(createBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var batchObj batch.BatchMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &batchObj); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outputs := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/v1/batches/"+batchObj.ID, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			var got batch.BatchMeta
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			outputs[idx] = got.OutputFileID
		}(i)
	}
	wg.Wait()
	if outputs[0] == "" || outputs[0] != outputs[1] {
		t.Fatalf("output ids: %v", outputs)
	}
}

func newBatchGatewayHandler(t *testing.T, cfHandler http.HandlerFunc) *Gateway {
	if cfHandler == nil {
		cfHandler = func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), "request_id") {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"result": map[string]interface{}{
						"responses": []map[string]interface{}{
							{
								"external_reference": "job-1",
								"success":            true,
								"result":             map[string]string{"response": "hello"},
							},
						},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"status":     "queued",
					"request_id": "cf-1",
				},
			})
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	batchDir := filepath.Join(home, "state", "polypus", "batch")
	t.Setenv("POLYPUS_BATCH_DIR", batchDir)
	cfSrv := httptest.NewServer(cfHandler)
	t.Cleanup(cfSrv.Close)
	dir := t.TempDir()
	yaml := `
batch_backend:
  enabled: true
  default: cf_local
chat_backend:
  enabled: true
  default: cf_local
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
proxy_backend:
  enabled: true
  default: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  cf_local:
    remote: true
    extension: cloudflare
    base_url: ` + cfSrv.URL + `/accounts/test/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, batch]
    models:
      allow:
        - "@cf/meta/llama-3.3-70b-instruct-fp8-fast"
        - "cf_local/@cf/meta/llama-3.3-70b-instruct-fp8-fast"
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("CF_AI_API_KEY", "test-token")
	rcfg, err := config.LoadRouterConfig(config.ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := router.NewClient(rcfg, router.WithCloudflareClientGet(func(b config.BackendDef) (*cloudflare.Client, error) {
		return cloudflare.NewClient(b)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	handler, err := NewHandler(config.ServeOptions{}, WithRouter(client))
	if err != nil {
		t.Fatal(err)
	}
	gw, ok := gatewayFromHandler(handler)
	if !ok {
		t.Fatal("expected *Gateway handler")
	}
	return gw
}
