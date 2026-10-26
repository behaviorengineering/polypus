package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

type cloudflareSubmitter struct {
	client func() (*cloudflare.Client, error)
	now    func() time.Time
}

// NewCloudflareSubmitter runs batches on Workers AI async batch. client builds (or
// fetches) the backend's client; now stamps the synthesised responses. Panics on nil.
func NewCloudflareSubmitter(client func() (*cloudflare.Client, error), now func() time.Time) Submitter {
	if client == nil || now == nil {
		panic("service.NewCloudflareSubmitter: nil client getter or clock")
	}
	return cloudflareSubmitter{client: client, now: now}
}

func (s cloudflareSubmitter) Validate(job Job) error {
	if !cloudflare.ModelSupportsBatch(job.Downstream) {
		return derrors.New(derrors.CodeInvalid, "batch.cloudflare", "model does not support cloudflare batch").
			With(keyModel, job.Downstream)
	}
	return nil
}

func (s cloudflareSubmitter) Submit(ctx context.Context, job Job, meta *batch.BatchMeta) error {
	client, err := s.client()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeNotReady, "batch.cloudflare", "client")
	}
	meta.CFModel = cloudflare.NormalizeModel(job.Downstream)
	id, err := client.SubmitBatch(ctx, job.Downstream, job.Endpoint, job.Lines)
	if err != nil {
		return err
	}
	meta.CFRequestID = id
	return nil
}

func (s cloudflareSubmitter) Poll(ctx context.Context, meta batch.BatchMeta) (Poll, error) {
	client, err := s.client()
	if err != nil {
		return Poll{}, derrors.Wrap(err, derrors.CodeNotReady, "batch.cloudflare", "client")
	}
	poll, err := client.PollBatch(ctx, meta.CFModel, meta.CFRequestID)
	if err != nil {
		return Poll{}, err
	}
	switch poll.State {
	case cloudflare.BatchPollCompleted:
		return Poll{State: PollDone, Lines: cloudflareLines(meta.Endpoint, meta.PublicModel, s.now().Unix(), poll.Responses)}, nil
	case cloudflare.BatchPollFailed:
		return Poll{State: PollFailed}, nil
	case cloudflare.BatchPollQueued, cloudflare.BatchPollRunning:
		return Poll{State: PollRunning}, nil
	default:
		return Poll{State: PollRunning}, nil
	}
}

func (s cloudflareSubmitter) Cancel(context.Context, batch.BatchMeta) error {
	return derrors.New(derrors.CodeUnimplemented, "batch.cloudflare", "workers ai batches cannot be cancelled")
}

// cloudflareLines turns Workers AI batch items into batch lines, synthesising the OpenAI
// response body for each success.
func cloudflareLines(endpoint, publicModel string, created int64, items []cloudflare.BatchResponseItem) []batch.LineResult {
	out := make([]batch.LineResult, 0, len(items))
	for _, item := range items {
		line := batch.LineResult{CustomID: item.ExternalReference}
		if !item.Success {
			line.ErrCode = "batch_failed"
			line.ErrMessage = firstNonEmpty(strings.TrimSpace(item.ErrorMessage), "workers ai batch line failed")
			out = append(out, line)
			continue
		}
		body, err := synthesizeOpenAIBody(endpoint, publicModel, created, item.Result)
		if err != nil {
			line.ErrCode = "synthesis_failed"
			line.ErrMessage = err.Error()
		} else {
			line.Body = body
		}
		out = append(out, line)
	}
	return out
}

func synthesizeOpenAIBody(endpoint, publicModel string, created int64, result json.RawMessage) (json.RawMessage, error) {
	if len(result) == 0 {
		return nil, derrors.New(derrors.CodeUnavailable, "batch.cloudflare", "empty result")
	}
	switch endpoint {
	case batch.EndpointChatCompletions:
		return synthesizeChatBody(publicModel, created, result)
	case batch.EndpointEmbeddings:
		return synthesizeEmbedBody(publicModel, result)
	default:
		return nil, derrors.New(derrors.CodeInvalid, "batch.cloudflare", "unsupported endpoint").With("endpoint", endpoint)
	}
}

// chatCompletionBody is the OpenAI chat.completion a Workers AI {"response": ...}
// result becomes.
type chatCompletionBody struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatUsage is all zeros: Workers AI batch results carry no token counts.
type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// embeddingListBody is the OpenAI embedding list a bare Workers AI vector becomes.
type embeddingListBody struct {
	Object string          `json:"object"`
	Model  string          `json:"model"`
	Data   []embeddingItem `json:"data"`
}

type embeddingItem struct {
	Object    string          `json:"object"`
	Index     int             `json:"index"`
	Embedding json.RawMessage `json:"embedding"`
}

// synthesizeChatBody returns the result as a chat.completion: one that already has
// choices keeps them (relabelled), a {"response": "..."} one becomes a single message.
func synthesizeChatBody(publicModel string, created int64, result json.RawMessage) (json.RawMessage, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(result, &probe); err != nil {
		return nil, err
	}
	if raw, ok := probe["choices"]; ok && len(raw) > 0 {
		wrap := map[string]any{"id": "chatcmpl-batch", keyObject: "chat.completion"}
		for k, v := range probe {
			wrap[k] = v
		}
		wrap[keyModel] = publicModel
		wrap["created"] = created
		return json.Marshal(wrap)
	}
	var text struct {
		Response string `json:"response"`
	}
	content := ""
	if err := json.Unmarshal(result, &text); err == nil && strings.TrimSpace(text.Response) != "" {
		content = text.Response
	}
	return json.Marshal(chatCompletionBody{
		ID:      "chatcmpl-batch",
		Object:  "chat.completion",
		Created: created,
		Model:   publicModel,
		Choices: []chatChoice{{Message: chatMessage{Role: "assistant", Content: content}, FinishReason: "stop"}},
	})
}

// synthesizeEmbedBody returns the result as an embedding list.
func synthesizeEmbedBody(publicModel string, result json.RawMessage) (json.RawMessage, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(result, &probe); err != nil {
		return nil, err
	}
	if raw, ok := probe["data"]; ok && len(raw) > 0 {
		return json.Marshal(map[string]any{keyObject: "list", keyModel: publicModel, "data": raw})
	}
	return json.Marshal(embeddingListBody{
		Object: "list",
		Model:  publicModel,
		Data:   []embeddingItem{{Object: "embedding", Embedding: result}},
	})
}
