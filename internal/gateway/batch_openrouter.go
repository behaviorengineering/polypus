package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/openrouter"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// refreshOpenRouterBatch polls an OpenRouter batch and finalizes it from the inline results
// once it completes. Failed, expired and cancelled batches end without output.
func (h batchesHandler) refreshOpenRouterBatch(ctx context.Context, backend config.BackendDef, meta batch.BatchMeta) (batch.BatchMeta, error) {
	var b openrouter.Batch
	if err := h.upstreams.Execute(meta.BackendID, func() error {
		client, err := openrouter.GetClient(backend)
		if err != nil {
			return err
		}
		b, err = client.GetBatch(ctx, meta.UpstreamBatchID)
		return err
	}); err != nil {
		return meta, err
	}
	now := h.batchNowTime().Unix()
	switch b.Status {
	case openrouter.StatusCompleted:
		okJSONL, errJSONL, completed, failed := buildBatchLineOutput(openRouterLines(b.Results, meta.PublicModel))
		return h.finalizeBatchOutput(meta, okJSONL, errJSONL, completed, failed)
	case openrouter.StatusFailed:
		meta.Status = batch.BatchStatusFailed
		meta.FailedAt = now
	case openrouter.StatusExpired:
		meta.Status = batch.BatchStatusExpired
	case openrouter.StatusCancelled:
		meta.Status = batch.BatchStatusCancelled
		meta.CancelledAt = now
	default:
		if meta.Status != batch.BatchStatusValidating {
			return meta, nil
		}
		meta.Status = batch.BatchStatusInProgress
	}
	if err := h.batchStore.UpdateBatch(meta); err != nil {
		return meta, err
	}
	return meta, nil
}

// openRouterLines turns OpenRouter results into batch lines. Success bodies already have the
// sync endpoint's OpenAI shape; only the model is relabelled to the public id.
func openRouterLines(results []openrouter.BatchResult, publicModel string) []router.BatchLineResult {
	out := make([]router.BatchLineResult, 0, len(results))
	for _, r := range results {
		line := router.BatchLineResult{CustomID: r.CustomID}
		switch {
		case r.Error != nil:
			line.ErrCode = firstNonEmpty(r.Error.Type, "batch_failed")
			line.ErrMessage = firstNonEmpty(r.Error.Message, "openrouter batch line failed")
		case r.Response == nil || r.Response.StatusCode < 200 || r.Response.StatusCode >= 300:
			line.ErrCode = "batch_failed"
			line.ErrMessage = "openrouter batch line returned no successful response"
		default:
			line.Body = relabelModel(r.Response.Body, publicModel)
		}
		out = append(out, line)
	}
	return out
}

func relabelModel(body json.RawMessage, publicModel string) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	model, err := json.Marshal(publicModel)
	if err != nil {
		return body
	}
	obj["model"] = model
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// rejectInlineMedia refuses lines OpenRouter batches cannot run: base64 or data-URI images
// and audio input. Public https image URLs pass; whether the provider accepts them is its call.
func rejectInlineMedia(lines []batch.InputLine) error {
	for _, line := range lines {
		var body struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(line.Body, &body); err != nil {
			continue
		}
		for _, m := range body.Messages {
			var parts []struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
			if err := json.Unmarshal(m.Content, &parts); err != nil {
				continue
			}
			for _, p := range parts {
				inlineImage := p.Type == "image_url" && strings.HasPrefix(strings.TrimSpace(p.ImageURL.URL), "data:")
				if inlineImage || p.Type == "input_audio" {
					return derrors.New(derrors.CodeInvalid, "gateway.rejectInlineMedia",
						"openrouter batches take text and public https image URLs only (no base64 images or audio)").
						With("custom_id", line.CustomID)
				}
			}
		}
	}
	return nil
}
