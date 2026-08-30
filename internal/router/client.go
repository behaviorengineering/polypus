package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/extension/cloudflare"
	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// Client wraps Bifrost for Polypus multi-backend routing (speech, chat, embed).
type Client struct {
	bf    *bifrost.Bifrost
	reg   *Registry
	cfGet func(config.BackendDef) (*cloudflare.Client, error)
}

// CloudflareClientGet optionally overrides process-scoped cloudflare.GetClient (tests).
type CloudflareClientGet func(config.BackendDef) (*cloudflare.Client, error)

// NewClient validates backends and initializes Bifrost.
func NewClient(cfg config.RouterConfig, opts ...ClientOption) (*Client, error) {
	reg, err := NewRegistry(cfg)
	if err != nil {
		return nil, err
	}
	var co clientOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&co)
		}
	}
	account := NewAccount(cfg)
	bf, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account: account,
	})
	if err != nil {
		return nil, fmt.Errorf("bifrost init: %w", err)
	}
	cfGet := co.cfGet
	if cfGet == nil {
		cfGet = cloudflare.GetClient
	}
	return &Client{bf: bf, reg: reg, cfGet: cfGet}, nil
}

type clientOptions struct {
	cfGet CloudflareClientGet
}

// ClientOption configures NewClient.
type ClientOption func(*clientOptions)

// WithCloudflareClientGet injects CF client construction (defaults to GetClient).
func WithCloudflareClientGet(fn CloudflareClientGet) ClientOption {
	return func(o *clientOptions) {
		o.cfGet = fn
	}
}

func (c *Client) cloudflareClient(b config.BackendDef) (*cloudflare.Client, error) {
	if c != nil && c.cfGet != nil {
		return c.cfGet(b)
	}
	return cloudflare.GetClient(b)
}

// Close shuts down the Bifrost client.
func (c *Client) Close() {
	if c != nil && c.bf != nil {
		c.bf.Shutdown()
	}
}

// Registry returns the routing table.
func (c *Client) Registry() *Registry {
	return c.reg
}

// UsesBifrost reports whether leaf chat/embed (or the Switchyard hop) for backendID
// should go through Bifrost. Cloudflare TTS/STT stay on the extension (/run).
func (c *Client) UsesBifrost(backendID string) bool {
	if c == nil || c.reg == nil {
		return false
	}
	if backendID == ProviderSwitchyard {
		return shouldRegisterSwitchyard(c.reg.Config())
	}
	_, ok := c.reg.Backend(backendID)
	return ok
}

func bifrostRawContext(parent context.Context, timeout time.Duration) *schemas.BifrostContext {
	deadline := schemas.NoDeadline
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	bctx := schemas.NewBifrostContext(parent, deadline)
	bctx.SetValue(schemas.BifrostContextKeyUseRawRequestBody, true)
	return bctx
}

// ChatCompletionRaw sends an OpenAI-shaped chat body via Bifrost (non-streaming).
func (c *Client) ChatCompletionRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) ([]byte, error) {
	if c == nil || c.bf == nil {
		return nil, fmt.Errorf("bifrost not configured")
	}
	var envelope struct {
		Messages []schemas.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse chat body: %w", err)
	}
	if len(envelope.Messages) == 0 {
		return nil, fmt.Errorf("messages required")
	}
	bctx := bifrostRawContext(ctx, timeout)
	resp, berr := c.bf.ChatCompletionRequest(bctx, &schemas.BifrostChatRequest{
		Provider:       schemas.ModelProvider(backendID),
		Model:          model,
		Input:          envelope.Messages,
		RawRequestBody: body,
	})
	if berr != nil {
		return nil, bifrostErr(berr)
	}
	if resp == nil {
		return nil, fmt.Errorf("empty chat response from backend")
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal chat response: %w", err)
	}
	return out, nil
}

// ChatCompletionStreamRaw streams OpenAI-shaped chat via Bifrost.
// Each channel value is one JSON chat.completion.chunk object (not SSE-framed).
// The channel is closed when the stream ends; streamErr receives at most one terminal error.
func (c *Client) ChatCompletionStreamRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) (<-chan []byte, <-chan error, error) {
	if c == nil || c.bf == nil {
		return nil, nil, fmt.Errorf("bifrost not configured")
	}
	var envelope struct {
		Messages []schemas.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, fmt.Errorf("parse chat body: %w", err)
	}
	if len(envelope.Messages) == 0 {
		return nil, nil, fmt.Errorf("messages required")
	}
	// Streams use client cancel only (no Bifrost wall-clock deadline), matching hop
	// timeout rules on the HTTP proxy path. The timeout arg is reserved for API symmetry.
	_ = timeout
	bctx := bifrostRawContext(ctx, 0)
	stream, berr := c.bf.ChatCompletionStreamRequest(bctx, &schemas.BifrostChatRequest{
		Provider:       schemas.ModelProvider(backendID),
		Model:          model,
		Input:          envelope.Messages,
		RawRequestBody: body,
	})
	if berr != nil {
		return nil, nil, bifrostErr(berr)
	}
	out := make(chan []byte, 16)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		for chunk := range stream {
			if chunk == nil {
				continue
			}
			if chunk.BifrostError != nil {
				errCh <- bifrostErr(chunk.BifrostError)
				return
			}
			if chunk.BifrostChatResponse == nil {
				continue
			}
			raw, err := json.Marshal(chunk.BifrostChatResponse)
			if err != nil {
				errCh <- fmt.Errorf("marshal stream chunk: %w", err)
				return
			}
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			case out <- raw:
			}
		}
	}()
	return out, errCh, nil
}

// EmbeddingRaw sends an OpenAI-shaped embeddings body via Bifrost.
func (c *Client) EmbeddingRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) ([]byte, error) {
	if c == nil || c.bf == nil {
		return nil, fmt.Errorf("bifrost not configured")
	}
	input, err := parseEmbeddingInput(body)
	if err != nil {
		return nil, err
	}
	bctx := bifrostRawContext(ctx, timeout)
	resp, berr := c.bf.EmbeddingRequest(bctx, &schemas.BifrostEmbeddingRequest{
		Provider:       schemas.ModelProvider(backendID),
		Model:          model,
		Input:          input,
		RawRequestBody: body,
	})
	if berr != nil {
		return nil, bifrostErr(berr)
	}
	if resp == nil {
		return nil, fmt.Errorf("empty embedding response from backend")
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding response: %w", err)
	}
	return out, nil
}

func parseEmbeddingInput(body []byte) (*schemas.EmbeddingInput, error) {
	var envelope struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse embedding body: %w", err)
	}
	if len(envelope.Input) == 0 || string(envelope.Input) == "null" {
		return nil, fmt.Errorf("input required")
	}
	var text string
	if err := json.Unmarshal(envelope.Input, &text); err == nil {
		return &schemas.EmbeddingInput{Text: &text}, nil
	}
	var texts []string
	if err := json.Unmarshal(envelope.Input, &texts); err == nil {
		return &schemas.EmbeddingInput{Texts: texts}, nil
	}
	return nil, fmt.Errorf("input must be string or string array")
}

// SpeechRequest is a normalized OpenAI TTS request.
type SpeechRequest struct {
	Model          string
	Input          string
	Voice          string
	ResponseFormat string
	Speed          *float64
}

// TranscriptionRequest is a normalized OpenAI STT request.
type TranscriptionRequest struct {
	Model          string
	Audio          []byte
	Filename       string
	ResponseFormat string
	Language       string
}

// Synthesize runs TTS via the resolved backend.
func (c *Client) Synthesize(ctx context.Context, req SpeechRequest) ([]byte, error) {
	if strings.TrimSpace(req.Input) == "" {
		return nil, fmt.Errorf("input required")
	}
	backendID, downstream, err := c.reg.ResolveTTS(req.Model)
	if err != nil {
		return nil, err
	}
	b, ok := c.reg.Backend(string(backendID))
	if !ok {
		return nil, fmt.Errorf("backend %q not found", backendID)
	}
	if b.IsCloudflareExtension() {
		cf, err := c.cloudflareClient(b)
		if err != nil {
			return nil, err
		}
		model := downstream
		if model == "" {
			model = req.Model
		}
		audio, _, err := cf.Synthesize(ctx, cloudflare.SpeechRequest{
			Model:          model,
			Input:          req.Input,
			Voice:          req.Voice,
			ResponseFormat: req.ResponseFormat,
		})
		return audio, err
	}

	provider := backendID
	model := downstream
	if model == "" {
		model = strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_MODEL"))
	}
	if model == "" {
		model = strings.TrimSpace(req.Model)
	}
	params := &schemas.SpeechParameters{}
	if f := strings.TrimSpace(req.ResponseFormat); f != "" {
		params.ResponseFormat = f
	}
	if v := strings.TrimSpace(req.Voice); v != "" {
		params.VoiceConfig = &schemas.SpeechVoiceInput{Voice: schemas.Ptr(v)}
	}
	if req.Speed != nil {
		params.Speed = req.Speed
	}
	deadline := schemas.NoDeadline
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl
	}
	bctx := schemas.NewBifrostContext(ctx, deadline)
	resp, berr := c.bf.SpeechRequest(bctx, &schemas.BifrostSpeechRequest{
		Provider: provider,
		Model:    model,
		Input:    &schemas.SpeechInput{Input: req.Input},
		Params:   params,
	})
	if berr != nil {
		return nil, bifrostErr(berr)
	}
	if resp == nil || len(resp.Audio) == 0 {
		return nil, fmt.Errorf("empty speech audio from backend")
	}
	return resp.Audio, nil
}

// Transcribe runs STT via the resolved backend.
func (c *Client) Transcribe(ctx context.Context, req TranscriptionRequest) ([]byte, string, error) {
	if len(req.Audio) == 0 {
		return nil, "", fmt.Errorf("audio file required")
	}
	backendID, downstream, err := c.reg.ResolveSTT(req.Model)
	if err != nil {
		return nil, "", err
	}
	b, ok := c.reg.Backend(string(backendID))
	if !ok {
		return nil, "", fmt.Errorf("backend %q not found", backendID)
	}
	if b.IsCloudflareExtension() {
		cf, err := c.cloudflareClient(b)
		if err != nil {
			return nil, "", err
		}
		model := downstream
		if model == "" {
			model = req.Model
		}
		text, err := cf.Transcribe(ctx, cloudflare.TranscriptionRequest{
			Model:    model,
			Audio:    req.Audio,
			Filename: req.Filename,
			Language: req.Language,
		})
		if err != nil {
			return nil, "", err
		}
		format := strings.TrimSpace(req.ResponseFormat)
		if format == "" {
			format = "json"
		}
		if schemas.IsPlainTextTranscriptionFormat(&format) {
			return []byte(text), "text/plain; charset=utf-8", nil
		}
		body := fmt.Sprintf(`{"text":%q}`, text)
		return []byte(body), "application/json", nil
	}

	provider := backendID
	model := downstream
	if model == "" {
		model = strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_STT_MODEL"))
	}
	if model == "" {
		model = strings.TrimSpace(req.Model)
	}
	filename := strings.TrimSpace(req.Filename)
	if filename == "" {
		filename = "audio.wav"
	}
	format := strings.TrimSpace(req.ResponseFormat)
	if format == "" {
		format = "json"
	}
	params := &schemas.TranscriptionParameters{
		ResponseFormat: schemas.Ptr(format),
	}
	if lang := strings.TrimSpace(req.Language); lang != "" {
		params.Language = schemas.Ptr(lang)
	}
	deadline := schemas.NoDeadline
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl
	}
	bctx := schemas.NewBifrostContext(ctx, deadline)
	resp, berr := c.bf.TranscriptionRequest(bctx, &schemas.BifrostTranscriptionRequest{
		Provider: provider,
		Model:    model,
		Input: &schemas.TranscriptionInput{
			File:     req.Audio,
			Filename: filename,
		},
		Params: params,
	})
	if berr != nil {
		return nil, "", bifrostErr(berr)
	}
	if resp == nil {
		return nil, "", fmt.Errorf("empty transcription from backend")
	}
	if schemas.IsPlainTextTranscriptionFormat(resp.ResponseFormat) {
		return []byte(resp.Text), "text/plain; charset=utf-8", nil
	}
	body := fmt.Sprintf(`{"text":%q}`, resp.Text)
	return []byte(body), "application/json", nil
}

func bifrostErr(berr *schemas.BifrostError) error {
	if berr == nil {
		return fmt.Errorf("bifrost error")
	}
	msg := "request failed"
	if berr.Error != nil && strings.TrimSpace(berr.Error.Message) != "" {
		msg = strings.TrimSpace(berr.Error.Message)
	}
	if berr.StatusCode != nil {
		return fmt.Errorf("bifrost: %s (status %d)", msg, *berr.StatusCode)
	}
	return fmt.Errorf("bifrost: %s", msg)
}
