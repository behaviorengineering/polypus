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

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/httpx"
)

const (
	batchSmokeCustomID    = "smoke-batch-1"
	batchSmokePollEvery   = 2 * time.Second
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
	if ping.Status == StatusFail || lifecycle.Status == StatusFail {
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

	line, err := json.Marshal(struct {
		CustomID string      `json:"custom_id"`
		Method   string      `json:"method"`
		URL      string      `json:"url"`
		Body     pingRequest `json:"body"`
	}{batchSmokeCustomID, http.MethodPost, batch.EndpointChatCompletions, newPingRequest(model, opts)})
	if err != nil {
		return "", fmt.Errorf("batch line: encode: %w", err)
	}
	fileID, err := uploadBatchJSONL(ctx, opts.BaseURL, string(line)+"\n")
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
	case batch.StatusCompleted:
		// continue.
	case batch.StatusFailed, batch.StatusExpired, batch.StatusCancelled:
		return "", fmt.Errorf("batch terminal status %s", meta.Status)
	case batch.StatusValidating, batch.StatusInProgress, batch.StatusCancelling:
		return "", fmt.Errorf("batch not finished: %s", meta.Status)
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
		return "", fmt.Errorf("output missing custom_id %q: %s", batchSmokeCustomID, httpx.Truncate(string(raw), 80))
	}
	return fmt.Sprintf("batch %s completed", batchID), nil
}

func uploadBatchJSONL(ctx context.Context, baseURL, content string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("purpose", "batch"); err != nil {
		return "", fmt.Errorf("files upload: multipart: %w", err)
	}
	fw, err := w.CreateFormFile("file", "smoke-batch.jsonl")
	if err != nil {
		return "", fmt.Errorf("files upload: multipart: %w", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		return "", fmt.Errorf("files upload: multipart: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("files upload: multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/files", &buf)
	if err != nil {
		return "", fmt.Errorf("files upload: request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	raw, _, err := call(req, "files upload", probeBodyMax)
	if err != nil {
		return "", err
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("files upload: decode: %w", err)
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return "", fmt.Errorf("files upload: empty id")
	}
	return parsed.ID, nil
}

// batchCreateRequest is the OpenAI batch create body.
type batchCreateRequest struct {
	InputFileID      string `json:"input_file_id"`
	Endpoint         string `json:"endpoint"`
	CompletionWindow string `json:"completion_window"`
}

func createBatch(ctx context.Context, baseURL, inputFileID string) (string, error) {
	body, err := json.Marshal(batchCreateRequest{
		InputFileID:      inputFileID,
		Endpoint:         batch.EndpointChatCompletions,
		CompletionWindow: batchCompletionWindow,
	})
	if err != nil {
		return "", fmt.Errorf("batches create: encode: %w", err)
	}
	req, err := httpNewPost(ctx, baseURL+"/v1/batches", string(body))
	if err != nil {
		return "", fmt.Errorf("batches create: request: %w", err)
	}
	raw, _, err := call(req, "batches create", probeBodyMax)
	if err != nil {
		return "", err
	}
	var parsed struct {
		ID     string       `json:"id"`
		Status batch.Status `json:"status"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("batches create: decode: %w", err)
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return "", fmt.Errorf("batches create: empty id")
	}
	switch parsed.Status {
	case batch.StatusValidating, batch.StatusInProgress, batch.StatusCompleted:
		return parsed.ID, nil
	case batch.StatusFailed, batch.StatusExpired, batch.StatusCancelling, batch.StatusCancelled:
		return "", fmt.Errorf("batches create terminal status %s", parsed.Status)
	default:
		return "", fmt.Errorf("batches create unexpected status %s", parsed.Status)
	}
}

type batchMetaSmoke struct {
	ID           string       `json:"id"`
	Status       batch.Status `json:"status"`
	OutputFileID string       `json:"output_file_id"`
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
		case batch.StatusCompleted, batch.StatusFailed, batch.StatusExpired, batch.StatusCancelled:
			return meta, nil
		case batch.StatusValidating, batch.StatusInProgress, batch.StatusCancelling:
			// keep polling.
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
		return zero, fmt.Errorf("batches get: request: %w", err)
	}
	raw, _, err := call(req, "batches get", probeBodyMax)
	if err != nil {
		return zero, err
	}
	var meta batchMetaSmoke
	if err := json.Unmarshal(raw, &meta); err != nil {
		return zero, fmt.Errorf("batches get: decode: %w", err)
	}
	return meta, nil
}

func downloadFileContent(ctx context.Context, baseURL, fileID string) ([]byte, error) {
	req, err := httpNewGet(ctx, baseURL+"/v1/files/"+fileID+"/content")
	if err != nil {
		return nil, fmt.Errorf("files content: request: %w", err)
	}
	raw, _, err := call(req, "files content", probeBodyMax)
	return raw, err
}
