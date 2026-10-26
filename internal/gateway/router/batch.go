package router

import (
	"context"
	"encoding/json"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	bfanthropic "github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/schemas"
)

// BatchPoll is the state of an upstream Claude Message Batch.
type BatchPoll struct {
	// Ended is true once Anthropic has finished processing (succeeded, errored, cancelled or expired).
	Ended bool
	// Cancelling is true while a cancel requested upstream is still settling.
	Cancelling bool
}

// ClaudeBatches runs Anthropic Message Batches through a router client's Bifrost
// instance. It is separate from Client, which serves the synchronous calls; the batch
// service owns it.
type ClaudeBatches struct {
	c *Client
}

// NewClaudeBatches returns the Message Batches surface of c. Panics if c is nil.
func NewClaudeBatches(c *Client) *ClaudeBatches {
	if c == nil {
		panic("router.NewClaudeBatches: nil client")
	}
	return &ClaudeBatches{c: c}
}

// Create submits OpenAI chat batch lines to an Anthropic backend as one Message Batch and
// returns its upstream id. Each line's OpenAI body becomes native Messages params through
// Bifrost's own chat converter, with the backend's automatic prompt caching applied.
func (b *ClaudeBatches) Create(ctx context.Context, backendID, model string, lines []batch.InputLine, timeout time.Duration) (string, error) {
	c := b.c
	if c == nil || c.bf == nil {
		return "", derrors.New(derrors.CodeFailedPrecondition, "router.ClaudeBatches.Create", "bifrost not configured")
	}
	bctx := c.bifrostContext(ctx, timeout, false)
	items := make([]schemas.BatchRequestItem, 0, len(lines))
	for _, line := range lines {
		params, err := c.claudeBatchParams(bctx, backendID, model, line.Body)
		if err != nil {
			return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "router.ClaudeBatches.Create", "translate line").
				With("custom_id", line.CustomID)
		}
		items = append(items, schemas.BatchRequestItem{CustomID: line.CustomID, Params: params})
	}
	resp, berr := c.bf.BatchCreateRequest(bctx, &schemas.BifrostBatchCreateRequest{
		Provider: schemas.ModelProvider(backendID),
		Model:    &model,
		Requests: items,
		Endpoint: schemas.BatchEndpointMessages,
	})
	if berr != nil {
		return "", c.chatBifrostErr(backendID, berr, bifrostProviderHeader(bctx))
	}
	if resp == nil || resp.ID == "" {
		return "", derrors.New(derrors.CodeUnavailable, "router.ClaudeBatches.Create", "empty batch response")
	}
	return resp.ID, nil
}

func (c *Client) claudeBatchParams(bctx *schemas.BifrostContext, backendID, model string, body []byte) (map[string]any, error) {
	const op = "router.claudeBatchParams"
	messages, err := parseChatMessages(op, body)
	if err != nil {
		return nil, err
	}
	req, err := bifrostChatRequest(backendID, model, messages, body, false)
	if err != nil {
		return nil, err
	}
	c.applyAutoPromptCache(backendID, body, req)
	msgReq, err := bfanthropic.ToAnthropicChatRequest(bctx, req)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, op, "convert to messages params")
	}
	// A failure past here is a conversion bug in Polypus, not the caller's input.
	raw, err := json.Marshal(msgReq)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, op, "marshal messages params")
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, op, "decode messages params")
	}
	// Message Batches reject streaming; a batch line always runs to completion.
	delete(params, "stream")
	return params, nil
}

// Status reports whether an upstream Message Batch has ended or is cancelling.
func (b *ClaudeBatches) Status(ctx context.Context, backendID, batchID string, timeout time.Duration) (BatchPoll, error) {
	c := b.c
	if c == nil || c.bf == nil {
		return BatchPoll{}, derrors.New(derrors.CodeFailedPrecondition, "router.ClaudeBatches.Status", "bifrost not configured")
	}
	bctx := c.bifrostContext(ctx, timeout, false)
	resp, berr := c.bf.BatchRetrieveRequest(bctx, &schemas.BifrostBatchRetrieveRequest{
		Provider: schemas.ModelProvider(backendID),
		BatchID:  batchID,
	})
	if berr != nil {
		return BatchPoll{}, c.chatBifrostErr(backendID, berr, bifrostProviderHeader(bctx))
	}
	if resp == nil {
		return BatchPoll{}, derrors.New(derrors.CodeUnavailable, "router.ClaudeBatches.Status", "empty batch response")
	}
	return BatchPoll{
		Ended:      resp.Status == schemas.BatchStatusEnded,
		Cancelling: resp.Status == schemas.BatchStatusCancelling,
	}, nil
}

// Results fetches an ended Message Batch and translates each succeeded line into an OpenAI
// chat.completion labelled with publicModel.
func (b *ClaudeBatches) Results(ctx context.Context, backendID, batchID, publicModel string, timeout time.Duration) ([]batch.LineResult, error) {
	c := b.c
	if c == nil || c.bf == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "router.ClaudeBatches.Results", "bifrost not configured")
	}
	bctx := c.bifrostContext(ctx, timeout, false)
	resp, berr := c.bf.BatchResultsRequest(bctx, &schemas.BifrostBatchResultsRequest{
		Provider: schemas.ModelProvider(backendID),
		BatchID:  batchID,
	})
	if berr != nil {
		return nil, c.chatBifrostErr(backendID, berr, bifrostProviderHeader(bctx))
	}
	if resp == nil {
		return nil, derrors.New(derrors.CodeUnavailable, "router.ClaudeBatches.Results", "empty batch results")
	}
	out := make([]batch.LineResult, 0, len(resp.Results))
	for _, item := range resp.Results {
		out = append(out, claudeBatchLine(bctx, item, publicModel))
	}
	return out, nil
}

func claudeBatchLine(bctx *schemas.BifrostContext, item schemas.BatchResultItem, publicModel string) batch.LineResult {
	line := batch.LineResult{CustomID: item.CustomID}
	if item.Failed() || item.Result == nil {
		line.ErrCode = "batch_failed"
		if item.Result != nil && item.Result.Type != "" {
			line.ErrCode = item.Result.Type
		}
		if item.Error != nil {
			line.ErrMessage = item.Error.Message
		}
		if line.ErrMessage == "" {
			line.ErrMessage = "claude batch line did not succeed"
		}
		return line
	}
	body, err := claudeMessageToChatCompletion(bctx, item.Result.Message, publicModel)
	if err != nil {
		line.ErrCode = "synthesis_failed"
		line.ErrMessage = err.Error()
		return line
	}
	line.Body = body
	return line
}

func claudeMessageToChatCompletion(bctx *schemas.BifrostContext, message map[string]any, publicModel string) (json.RawMessage, error) {
	raw, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	var msg bfanthropic.AnthropicMessageResponse
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	chat := msg.ToBifrostChatResponse(bctx)
	if chat == nil {
		return nil, derrors.New(derrors.CodeInternal, "router.claudeMessageToChatCompletion", "empty message")
	}
	chat.Model = publicModel
	return json.Marshal(chat)
}

// Cancel asks Anthropic to cancel a Message Batch. Lines already finished keep their
// results; the batch ends once in-flight lines settle.
func (b *ClaudeBatches) Cancel(ctx context.Context, backendID, batchID string, timeout time.Duration) error {
	c := b.c
	if c == nil || c.bf == nil {
		return derrors.New(derrors.CodeFailedPrecondition, "router.ClaudeBatches.Cancel", "bifrost not configured")
	}
	bctx := c.bifrostContext(ctx, timeout, false)
	_, berr := c.bf.BatchCancelRequest(bctx, &schemas.BifrostBatchCancelRequest{
		Provider: schemas.ModelProvider(backendID),
		BatchID:  batchID,
	})
	if berr != nil {
		return c.chatBifrostErr(backendID, berr, bifrostProviderHeader(bctx))
	}
	return nil
}
