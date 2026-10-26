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

// BatchLineResult is one batch line translated back to the OpenAI shape.
type BatchLineResult struct {
	CustomID string
	// Body is an OpenAI chat.completion when the line succeeded.
	Body json.RawMessage
	// ErrCode and ErrMessage describe a failed line (errored, cancelled or expired).
	ErrCode    string
	ErrMessage string
}

// ClaudeBatchCreate submits OpenAI chat batch lines to an Anthropic backend as one Message
// Batch and returns its upstream id. Each line's OpenAI body becomes native Messages params
// through Bifrost's own chat converter, with the backend's automatic prompt caching applied.
func (c *Client) ClaudeBatchCreate(ctx context.Context, backendID, model string, lines []batch.InputLine, timeout time.Duration) (string, error) {
	if c == nil || c.bf == nil {
		return "", derrors.New(derrors.CodeFailedPrecondition, "router.ClaudeBatchCreate", "bifrost not configured")
	}
	bctx := c.bifrostContext(ctx, timeout, false)
	items := make([]schemas.BatchRequestItem, 0, len(lines))
	for _, line := range lines {
		params, err := c.claudeBatchParams(bctx, backendID, model, line.Body)
		if err != nil {
			return "", derrors.Wrap(err, derrors.CodeInvalid, "router.ClaudeBatchCreate", "translate line").
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
		return "", derrors.New(derrors.CodeUnavailable, "router.ClaudeBatchCreate", "empty batch response")
	}
	return resp.ID, nil
}

func (c *Client) claudeBatchParams(bctx *schemas.BifrostContext, backendID, model string, body []byte) (map[string]any, error) {
	var envelope struct {
		Messages []schemas.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Messages) == 0 {
		return nil, derrors.New(derrors.CodeInvalid, "router.claudeBatchParams", "messages required")
	}
	req, err := bifrostChatRequest(backendID, model, envelope.Messages, body, false)
	if err != nil {
		return nil, err
	}
	c.applyAutoPromptCache(backendID, body, req)
	msgReq, err := bfanthropic.ToAnthropicChatRequest(bctx, req)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(msgReq)
	if err != nil {
		return nil, err
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	// Message Batches reject streaming; a batch line always runs to completion.
	delete(params, "stream")
	return params, nil
}

// ClaudeBatchStatus reports whether an upstream Message Batch has ended or is cancelling.
func (c *Client) ClaudeBatchStatus(ctx context.Context, backendID, batchID string, timeout time.Duration) (BatchPoll, error) {
	if c == nil || c.bf == nil {
		return BatchPoll{}, derrors.New(derrors.CodeFailedPrecondition, "router.ClaudeBatchStatus", "bifrost not configured")
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
		return BatchPoll{}, derrors.New(derrors.CodeUnavailable, "router.ClaudeBatchStatus", "empty batch response")
	}
	return BatchPoll{
		Ended:      resp.Status == schemas.BatchStatusEnded,
		Cancelling: resp.Status == schemas.BatchStatusCancelling,
	}, nil
}

// ClaudeBatchResults fetches an ended Message Batch and translates each succeeded line into
// an OpenAI chat.completion labelled with publicModel.
func (c *Client) ClaudeBatchResults(ctx context.Context, backendID, batchID, publicModel string, timeout time.Duration) ([]BatchLineResult, error) {
	if c == nil || c.bf == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "router.ClaudeBatchResults", "bifrost not configured")
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
		return nil, derrors.New(derrors.CodeUnavailable, "router.ClaudeBatchResults", "empty batch results")
	}
	out := make([]BatchLineResult, 0, len(resp.Results))
	for _, item := range resp.Results {
		out = append(out, claudeBatchLine(bctx, item, publicModel))
	}
	return out, nil
}

func claudeBatchLine(bctx *schemas.BifrostContext, item schemas.BatchResultItem, publicModel string) BatchLineResult {
	line := BatchLineResult{CustomID: item.CustomID}
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

// ClaudeBatchCancel asks Anthropic to cancel a Message Batch. Lines already finished keep
// their results; the batch ends once in-flight lines settle.
func (c *Client) ClaudeBatchCancel(ctx context.Context, backendID, batchID string, timeout time.Duration) error {
	if c == nil || c.bf == nil {
		return derrors.New(derrors.CodeFailedPrecondition, "router.ClaudeBatchCancel", "bifrost not configured")
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
