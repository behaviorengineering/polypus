package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/openrouter"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

type openRouterSubmitter struct {
	client func() (*openrouter.Client, error)
}

// NewOpenRouterSubmitter runs batches on OpenRouter's Batch API. client builds (or
// fetches) the backend's client. Panics if client is nil.
func NewOpenRouterSubmitter(client func() (*openrouter.Client, error)) Submitter {
	if client == nil {
		panic("service.NewOpenRouterSubmitter: nil client getter")
	}
	return openRouterSubmitter{client: client}
}

// Validate refuses lines OpenRouter batches cannot run: base64 or data-URI images and
// audio input. Public https image URLs pass; whether the provider accepts them is its call.
func (s openRouterSubmitter) Validate(job Job) error {
	for _, line := range job.Lines {
		if hasInlineMedia(line.Body) {
			return derrors.New(derrors.CodeInvalid, "batch.openrouter",
				"openrouter batches take text and public https image URLs only (no base64 images or audio)").
				With("custom_id", line.CustomID)
		}
	}
	return nil
}

func hasInlineMedia(body json.RawMessage) bool {
	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	for _, m := range req.Messages {
		var parts []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if err := json.Unmarshal(m.Content, &parts); err != nil {
			continue // a plain string content has no media.
		}
		for _, p := range parts {
			if p.Type == "input_audio" || (p.Type == "image_url" && strings.HasPrefix(strings.TrimSpace(p.ImageURL.URL), "data:")) {
				return true
			}
		}
	}
	return false
}

func (s openRouterSubmitter) Submit(ctx context.Context, job Job, meta *batch.BatchMeta) error {
	client, err := s.client()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeNotReady, "batch.openrouter", "client")
	}
	id, err := client.SubmitBatch(ctx, job.Downstream, job.Endpoint, job.Lines)
	if err != nil {
		return err
	}
	meta.UpstreamBatchID = id
	return nil
}

func (s openRouterSubmitter) Poll(ctx context.Context, meta batch.BatchMeta) (Poll, error) {
	client, err := s.client()
	if err != nil {
		return Poll{}, derrors.Wrap(err, derrors.CodeNotReady, "batch.openrouter", "client")
	}
	b, err := client.GetBatch(ctx, meta.UpstreamBatchID)
	if err != nil {
		return Poll{}, err
	}
	switch b.Status {
	case openrouter.StatusCompleted:
		return Poll{State: PollDone, Lines: openRouterLines(b.Results, meta.PublicModel)}, nil
	case openrouter.StatusFailed:
		return Poll{State: PollFailed}, nil
	case openrouter.StatusExpired:
		return Poll{State: PollExpired}, nil
	case openrouter.StatusCancelled:
		return Poll{State: PollCancelled}, nil
	case openrouter.StatusValidating, openrouter.StatusInProgress, openrouter.StatusFinalizing:
		return Poll{State: PollRunning}, nil
	default:
		// A status OpenRouter adds later reads as still running until it is mapped.
		return Poll{State: PollRunning}, nil
	}
}

func (s openRouterSubmitter) Cancel(context.Context, batch.BatchMeta) error {
	return derrors.New(derrors.CodeUnimplemented, "batch.openrouter", "openrouter batches cannot be cancelled")
}

// openRouterLines turns OpenRouter results into batch lines. Success bodies already have
// the sync endpoint's OpenAI shape; only the model is relabelled to the public id.
func openRouterLines(results []openrouter.BatchResult, publicModel string) []batch.LineResult {
	out := make([]batch.LineResult, 0, len(results))
	for _, r := range results {
		line := batch.LineResult{CustomID: r.CustomID}
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

// relabelModel sets the body's model to the public id; a body that is not a JSON
// object is returned unchanged (BuildOutput then reports it).
func relabelModel(body json.RawMessage, publicModel string) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	model, err := json.Marshal(publicModel)
	if err != nil {
		return body
	}
	obj[keyModel] = model
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// OpenAI body keys.
const (
	keyModel  = "model"
	keyObject = "object"
)

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
