package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/extension/cloudflare"
	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// Client wraps Bifrost for Polypus multi-backend routing (speech, chat, embed).
type Client struct {
	bf               *bifrost.Bifrost
	reg              *Registry
	cfSpeechPluginOn bool
}

// CloudflareClientGet optionally overrides process-scoped cloudflare.GetClient (tests).
type CloudflareClientGet func(config.BackendDef) (*cloudflare.Client, error)

// NewClient validates backends and initializes Bifrost.
func NewClient(cfg config.RouterConfig, opts ...ClientOption) (*Client, error) {
	reg, err := NewRegistry(cfg)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "router.NewClient", "registry")
	}
	var co clientOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&co)
		}
	}
	cfGet := co.cfGet
	if cfGet == nil {
		cfGet = cloudflare.GetClient
	}
	account := NewAccount(cfg)
	var plugins []schemas.LLMPlugin
	if hasCloudflareBackend(cfg) {
		lookup := func(provider string) (config.BackendDef, bool) {
			return reg.Backend(provider)
		}
		plugins = append(plugins, cloudflare.NewRunSpeechPlugin(lookup, cloudflare.ClientGet(cfGet)))
	}
	bf, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account:    account,
		LLMPlugins: plugins,
	})
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "router.NewClient", "bifrost init")
	}
	return &Client{bf: bf, reg: reg, cfSpeechPluginOn: len(plugins) > 0}, nil
}

func hasCloudflareBackend(cfg config.RouterConfig) bool {
	for _, b := range cfg.Backends {
		if b.IsCloudflareExtension() {
			return true
		}
	}
	return false
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

// UsesBifrost reports whether leaf dials (or the Switchyard hop) for backendID
// should go through Bifrost. Cloudflare TTS/STT also enter Bifrost; a plugin
// short-circuits them onto extension /ai/run.
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
		return nil, derrors.New(derrors.CodeFailedPrecondition, "router.ChatCompletionRaw", "bifrost not configured")
	}
	var envelope struct {
		Messages []schemas.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "router.ChatCompletionRaw", "parse chat body")
	}
	if len(envelope.Messages) == 0 {
		return nil, derrors.New(derrors.CodeInvalid, "router.ChatCompletionRaw", "messages required")
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
		return nil, derrors.New(derrors.CodeUnavailable, "router.ChatCompletionRaw", "empty chat response from backend").
			With("backend", backendID).
			With("model", model)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "router.ChatCompletionRaw", "marshal chat response")
	}
	return out, nil
}

// ChatCompletionStreamRaw streams OpenAI-shaped chat via Bifrost.
// Each channel value is one JSON chat.completion.chunk object (not SSE-framed).
// The channel is closed when the stream ends; streamErr receives at most one terminal error.
func (c *Client) ChatCompletionStreamRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) (<-chan []byte, <-chan error, error) {
	if c == nil || c.bf == nil {
		return nil, nil, derrors.New(derrors.CodeFailedPrecondition, "router.ChatCompletionStreamRaw", "bifrost not configured")
	}
	var envelope struct {
		Messages []schemas.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, derrors.Wrap(err, derrors.CodeInvalid, "router.ChatCompletionStreamRaw", "parse chat body")
	}
	if len(envelope.Messages) == 0 {
		return nil, nil, derrors.New(derrors.CodeInvalid, "router.ChatCompletionStreamRaw", "messages required")
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
				errCh <- derrors.Wrap(err, derrors.CodeInternal, "router.ChatCompletionStreamRaw", "marshal stream chunk")
				return
			}
			select {
			case <-ctx.Done():
				errCh <- derrors.Wrap(ctx.Err(), derrors.CodeCanceled, "router.ChatCompletionStreamRaw", "context done")
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
		return nil, derrors.New(derrors.CodeFailedPrecondition, "router.EmbeddingRaw", "bifrost not configured")
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
		return nil, derrors.New(derrors.CodeUnavailable, "router.EmbeddingRaw", "empty embedding response from backend").
			With("backend", backendID).
			With("model", model)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "router.EmbeddingRaw", "marshal embedding response")
	}
	return out, nil
}

func parseEmbeddingInput(body []byte) (*schemas.EmbeddingInput, error) {
	var envelope struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "router.parseEmbeddingInput", "parse embedding body")
	}
	if len(envelope.Input) == 0 || string(envelope.Input) == "null" {
		return nil, derrors.New(derrors.CodeInvalid, "router.parseEmbeddingInput", "input required")
	}
	var text string
	if err := json.Unmarshal(envelope.Input, &text); err == nil {
		return &schemas.EmbeddingInput{Text: &text}, nil
	}
	var texts []string
	if err := json.Unmarshal(envelope.Input, &texts); err == nil {
		return &schemas.EmbeddingInput{Texts: texts}, nil
	}
	return nil, derrors.New(derrors.CodeInvalid, "router.parseEmbeddingInput", "input must be string or string array")
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
		return nil, derrors.New(derrors.CodeInvalid, "router.Synthesize", "input required")
	}
	backendID, downstream, err := c.reg.ResolveTTS(req.Model)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeNotFound, "router.Synthesize", "resolve tts")
	}
	if _, ok := c.reg.Backend(string(backendID)); !ok {
		return nil, derrors.New(derrors.CodeNotFound, "router.Synthesize", "backend not found").
			With("backend", string(backendID))
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
	ctx, cancel := ensureSpeechDeadline(ctx, c.reg.Config().Timeouts)
	defer cancel()
	deadline, _ := ctx.Deadline()
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
		return nil, derrors.New(derrors.CodeUnavailable, "router.Synthesize", "empty speech audio from backend")
	}
	return resp.Audio, nil
}

// Transcribe runs STT via the resolved backend.
func (c *Client) Transcribe(ctx context.Context, req TranscriptionRequest) ([]byte, string, error) {
	if len(req.Audio) == 0 {
		return nil, "", derrors.New(derrors.CodeInvalid, "router.Transcribe", "audio file required")
	}
	backendID, downstream, err := c.reg.ResolveSTT(req.Model)
	if err != nil {
		return nil, "", derrors.Wrap(err, derrors.CodeNotFound, "router.Transcribe", "resolve stt")
	}
	if _, ok := c.reg.Backend(string(backendID)); !ok {
		return nil, "", derrors.New(derrors.CodeNotFound, "router.Transcribe", "backend not found").
			With("backend", string(backendID))
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
	ctx, cancel := ensureSpeechDeadline(ctx, c.reg.Config().Timeouts)
	defer cancel()
	deadline, _ := ctx.Deadline()
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
		return nil, "", derrors.New(derrors.CodeUnavailable, "router.Transcribe", "empty transcription from backend")
	}
	if schemas.IsPlainTextTranscriptionFormat(resp.ResponseFormat) {
		return []byte(resp.Text), "text/plain; charset=utf-8", nil
	}
	body := fmt.Sprintf(`{"text":%q}`, resp.Text)
	return []byte(body), "application/json", nil
}

func ensureSpeechDeadline(ctx context.Context, timeouts config.Timeouts) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	d := time.Duration(timeouts.SpeechSeconds()) * time.Second
	return context.WithTimeout(ctx, d)
}

func bifrostErr(berr *schemas.BifrostError) error {
	if berr == nil {
		return derrors.New(derrors.CodeInternal, "router.bifrost", "bifrost error")
	}
	if berr.Error != nil && berr.Error.Error != nil {
		out := derrors.Wrap(berr.Error.Error, derrors.CodeUnavailable, "router.bifrost", "provider call")
		if berr.StatusCode != nil {
			out = out.With("status", fmt.Sprintf("%d", *berr.StatusCode))
		}
		return out
	}
	msg := "request failed"
	if berr.Error != nil && strings.TrimSpace(berr.Error.Message) != "" {
		msg = strings.TrimSpace(berr.Error.Message)
	}
	out := derrors.New(derrors.CodeUnavailable, "router.bifrost", msg)
	if berr.StatusCode != nil {
		out = out.With("status", fmt.Sprintf("%d", *berr.StatusCode))
	}
	return out
}
