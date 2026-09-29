package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

const (
	batchSmokeCustomID    = "smoke-batch-1"
	batchSmokePollEvery   = 2 * time.Second
	batchEndpointChat     = "/v1/chat/completions"
	batchCompletionWindow = "24h"
)

func runBatch(ctx context.Context, opts Options) ([]Result, error) {
	ping := timed(ChannelBatch, "ping", "", func() (string, error) {
		if err := pingHealth(ctx, opts.BaseURL); err != nil {
			return "", err
		}
		return "health ok", nil
	})
	lifecycle := timed(ChannelBatch, "batch_lifecycle", opts.BatchModel, func() (string, error) {
		return batchLifecycle(ctx, opts)
	})
	out := []Result{ping, lifecycle}
	var err error
	if ping.Status == "fail" || lifecycle.Status == "fail" {
		err = fmt.Errorf("batch smoke failed")
	}
	return out, err
}

func batchLifecycle(ctx context.Context, opts Options) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("batch smoke: context is required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return "", fmt.Errorf("batch smoke: missing deadline")
	}
	model := strings.TrimSpace(opts.BatchModel)
	if model == "" {
		return "", fmt.Errorf("batch model required")
	}

	line := fmt.Sprintf(
		`{"custom_id":%q,"method":"POST","url":%q,"body":{"model":%q,"messages":[{"role":"user","content":"Reply with the single word: ok"}],"max_tokens":%d,"temperature":%g}}`,
		batchSmokeCustomID, batchEndpointChat, model, opts.MaxTokens, opts.Temperature,
	)
	fileID, err := uploadBatchJSONL(ctx, opts.BaseURL, line+"\n")
	if err != nil {
		return "", err
	}

	batchID, err := createBatch(ctx, opts.BaseURL, fileID)
	if err != nil {
		return "", err
	}

	meta, err := pollBatchTerminal(ctx, opts.BaseURL, batchID)
	if err != nil {
		return "", err
	}
	switch meta.Status {
	case "completed":
		// continue
	case "failed", "expired", "cancelled":
		return "", fmt.Errorf("batch terminal status %s", meta.Status)
	default:
		return "", fmt.Errorf("unexpected batch status %s", meta.Status)
	}
	if strings.TrimSpace(meta.OutputFileID) == "" {
		return "", fmt.Errorf("completed batch missing output_file_id")
	}

	raw, err := downloadFileContent(ctx, opts.BaseURL, meta.OutputFileID)
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(raw), batchSmokeCustomID) {
		detail := strings.TrimSpace(string(raw))
		if len(detail) > 80 {
			detail = detail[:77] + "..."
		}
		return "", fmt.Errorf("output missing custom_id %q: %s", batchSmokeCustomID, detail)
	}
	return fmt.Sprintf("batch %s completed", batchID), nil
}

func uploadBatchJSONL(ctx context.Context, baseURL, content string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("purpose", "batch"); err != nil {
		return "", err
	}
	fw, err := w.CreateFormFile("file", "smoke-batch.jsonl")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/files", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := httpDefaultDo(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := ioReadAllLimit(resp.Body, 1<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("files upload status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return "", fmt.Errorf("files upload: empty id")
	}
	return parsed.ID, nil
}

func createBatch(ctx context.Context, baseURL, inputFileID string) (string, error) {
	body := fmt.Sprintf(
		`{"input_file_id":%q,"endpoint":%q,"completion_window":%q}`,
		inputFileID, batchEndpointChat, batchCompletionWindow,
	)
	req, err := httpNewPost(ctx, baseURL+"/v1/batches", body)
	if err != nil {
		return "", err
	}
	resp, err := httpDefaultDo(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := ioReadAllLimit(resp.Body, 1<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("batches create status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return "", fmt.Errorf("batches create: empty id")
	}
	switch parsed.Status {
	case "validating", "in_progress", "completed":
		return parsed.ID, nil
	case "failed", "expired", "cancelled":
		return "", fmt.Errorf("batches create terminal status %s", parsed.Status)
	default:
		return "", fmt.Errorf("batches create unexpected status %s", parsed.Status)
	}
}

type batchMetaSmoke struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	OutputFileID string `json:"output_file_id"`
}

func pollBatchTerminal(ctx context.Context, baseURL, batchID string) (batchMetaSmoke, error) {
	var last batchMetaSmoke
	for {
		if err := ctx.Err(); err != nil {
			return last, fmt.Errorf("batch poll: %w (last status %s)", err, last.Status)
		}
		meta, err := getBatch(ctx, baseURL, batchID)
		if err != nil {
			return last, err
		}
		last = meta
		switch meta.Status {
		case "completed", "failed", "expired", "cancelled":
			return meta, nil
		case "validating", "in_progress":
			// keep polling
		default:
			return meta, fmt.Errorf("batch poll unexpected status %s", meta.Status)
		}
		timer := time.NewTimer(batchSmokePollEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, fmt.Errorf("batch poll: %w (last status %s)", ctx.Err(), last.Status)
		case <-timer.C:
		}
	}
}

func getBatch(ctx context.Context, baseURL, batchID string) (batchMetaSmoke, error) {
	var zero batchMetaSmoke
	req, err := httpNewGet(ctx, baseURL+"/v1/batches/"+batchID)
	if err != nil {
		return zero, err
	}
	resp, err := httpDefaultDo(req)
	if err != nil {
		return zero, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := ioReadAllLimit(resp.Body, 1<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("batches get status %d: %s", resp.StatusCode, string(raw))
	}
	var meta batchMetaSmoke
	if err := json.Unmarshal(raw, &meta); err != nil {
		return zero, err
	}
	return meta, nil
}

func downloadFileContent(ctx context.Context, baseURL, fileID string) ([]byte, error) {
	req, err := httpNewGet(ctx, baseURL+"/v1/files/"+fileID+"/content")
	if err != nil {
		return nil, err
	}
	resp, err := httpDefaultDo(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := ioReadAllLimit(resp.Body, 1<<20)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("files content status %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}
