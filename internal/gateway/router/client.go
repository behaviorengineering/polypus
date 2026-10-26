package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/trace"
)

// Client wraps Bifrost for Polypus multi-backend routing (speech, chat, embed).
type Client struct {
	bf               *bifrost.Bifrost
	reg              *Registry
	cfSpeechPluginOn bool
	bfTracer         *observability.BifrostOTelTracer
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
	bfTracer := observability.NewBifrostOTelTracer()
	bf, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account:    account,
		LLMPlugins: plugins,
		Tracer:     bfTracer,
	})
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "router.NewClient", "bifrost init")
	}
	return &Client{bf: bf, reg: reg, cfSpeechPluginOn: len(plugins) > 0, bfTracer: bfTracer}, nil
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
	if c == nil {
		return
	}
	if c.bf != nil {
		c.bf.Shutdown()
	}
	if c.bfTracer != nil {
		c.bfTracer.Stop()
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

func bifrostRawContext(parent context.Context, timeout time.Duration, useRaw bool) *schemas.BifrostContext {
	deadline := schemas.NoDeadline
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	bctx := schemas.NewBifrostContext(parent, deadline)
	if useRaw {
		bctx.SetValue(schemas.BifrostContextKeyUseRawRequestBody, true)
	}
	return bctx
}

func (c *Client) bifrostContext(parent context.Context, timeout time.Duration, useRaw bool) *schemas.BifrostContext {
	bctx := bifrostRawContext(parent, timeout, useRaw)
	// The request's own trace and parent span ride on its context, so the shared
	// tracer never has to guess which request a Bifrost span belongs to.
	if sc := trace.SpanFromContext(parent).SpanContext(); sc.IsValid() {
		bctx.SetValue(schemas.BifrostContextKeyTraceID, sc.TraceID().String())
		bctx.SetValue(schemas.BifrostContextKeyParentSpanID, sc.SpanID().String())
	}
	return bctx
}

func (c *Client) bifrostUseRawChatBody(backendID string) bool {
	if c == nil || c.reg == nil {
		return true
	}
	b, ok := c.reg.Backend(backendID)
	if !ok {
		return true
	}
	return !b.IsGeminiExtension() && !b.IsAnthropicExtension()
}

func bifrostChatRequest(backendID, model string, messages []schemas.ChatMessage, body []byte, useRaw bool) (*schemas.BifrostChatRequest, error) {
	req := &schemas.BifrostChatRequest{
		Provider: schemas.ModelProvider(backendID),
		Model:    model,
		Input:    messages,
	}
	if useRaw {
		req.RawRequestBody = body
		return req, nil
	}
	params, err := chatParamsFromOpenAIBody(body)
	if err != nil {
		return nil, err
	}
	req.Params = params
	return req, nil
}

// applyAutoPromptCache adds a top-level ephemeral cache_control (the 5-minute lifetime)
// when the backend turns automatic caching on and the client sent no cache_control.
func (c *Client) applyAutoPromptCache(backendID string, body []byte, req *schemas.BifrostChatRequest) {
	if c == nil || c.reg == nil {
		return
	}
	b, ok := c.reg.Backend(backendID)
	if !ok {
		return
	}
	withAutoPromptCache(b, body, req)
}

func withAutoPromptCache(b config.BackendDef, body []byte, req *schemas.BifrostChatRequest) {
	if req == nil || req.RawRequestBody != nil || !b.AutoPromptCache() {
		return
	}
	// A quoted key cannot appear unescaped inside a JSON string value, so this
	// matches only a real cache_control field, at any depth (tools, system, messages).
	if bytes.Contains(body, []byte(`"cache_control"`)) {
		return
	}
	if req.Params == nil {
		req.Params = &schemas.ChatParameters{}
	}
	req.Params.CacheControl = &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral}
}

// chatParamsFromOpenAIBody reads every chat parameter Bifrost models (tools,
// tool_choice, sampling, stop, response_format, stream_options, top-level
// cache_control and reasoning) so native providers such as Gemini receive them.
// Legacy max_tokens maps to MaxCompletionTokens when max_completion_tokens is absent.
// Returns nil when the body sets no parameters.
func chatParamsFromOpenAIBody(body []byte) (*schemas.ChatParameters, error) {
	params := &schemas.ChatParameters{}
	if err := json.Unmarshal(body, params); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "router.chatParamsFromOpenAIBody", "parse chat params")
	}
	var legacy struct {
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.Unmarshal(body, &legacy); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "router.chatParamsFromOpenAIBody", "parse max_tokens")
	}
	if params.MaxCompletionTokens == nil && legacy.MaxTokens > 0 {
		n := legacy.MaxTokens
		params.MaxCompletionTokens = &n
	}
	if reflect.DeepEqual(*params, schemas.ChatParameters{}) {
		return nil, nil
	}
	return params, nil
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
	useRaw := c.bifrostUseRawChatBody(backendID)
	bctx := c.bifrostContext(ctx, timeout, useRaw)
	chatReq, err := bifrostChatRequest(backendID, model, envelope.Messages, body, useRaw)
	if err != nil {
		return nil, err
	}
	c.applyAutoPromptCache(backendID, body, chatReq)
	resp, berr := c.bf.ChatCompletionRequest(bctx, chatReq)
	if berr != nil {
		return nil, c.chatBifrostErr(backendID, berr, bifrostProviderHeader(bctx))
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
func (c *Client) ChatCompletionStreamRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) (<-chan ChatStreamChunk, <-chan error, error) {
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
	useRaw := c.bifrostUseRawChatBody(backendID)
	bctx := c.bifrostContext(ctx, 0, useRaw)
	chatReq, err := bifrostChatRequest(backendID, model, envelope.Messages, body, useRaw)
	if err != nil {
		return nil, nil, err
	}
	c.applyAutoPromptCache(backendID, body, chatReq)
	stream, berr := c.bf.ChatCompletionStreamRequest(bctx, chatReq)
	if berr != nil {
		return nil, nil, c.chatBifrostErr(backendID, berr, bifrostProviderHeader(bctx))
	}
	out := make(chan ChatStreamChunk, 16)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		for chunk := range stream {
			if chunk == nil {
				continue
			}
			if chunk.BifrostError != nil {
				errCh <- c.chatBifrostErr(backendID, chunk.BifrostError, bifrostProviderHeader(bctx))
				return
			}
			if chunk.BifrostChatResponse == nil {
				continue
			}
			item, err := chatStreamChunkFromResponse(chunk.BifrostChatResponse)
			if err != nil {
				errCh <- derrors.Wrap(err, derrors.CodeInternal, "router.ChatCompletionStreamRaw", "marshal stream chunk")
				return
			}
			select {
			case <-ctx.Done():
				errCh <- derrors.Wrap(ctx.Err(), derrors.CodeCanceled, "router.ChatCompletionStreamRaw", "context done")
				return
			case out <- item:
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
	input, err := parseEmbeddingInputItems(body)
	if err != nil {
		return nil, err
	}
	bctx := c.bifrostContext(ctx, timeout, true)
	resp, berr := c.bf.EmbeddingRequest(bctx, &schemas.BifrostEmbeddingRequest{
		Provider:       schemas.ModelProvider(backendID),
		Model:          model,
		Input:          input,
		RawRequestBody: body,
	})
	if berr != nil {
		return nil, bifrostErr(berr, bifrostProviderHeader(bctx))
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

func parseEmbeddingInputItems(body []byte) ([]schemas.EmbeddingInputItem, error) {
	var envelope struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "router.parseEmbeddingInputItems", "parse embedding body")
	}
	if len(envelope.Input) == 0 || string(envelope.Input) == "null" {
		return nil, derrors.New(derrors.CodeInvalid, "router.parseEmbeddingInputItems", "input required")
	}
	var text string
	if err := json.Unmarshal(envelope.Input, &text); err == nil {
		return []schemas.EmbeddingInputItem{{
			Content: schemas.EmbeddingContent{{
				Type: schemas.EmbeddingContentPartTypeText,
				Text: &text,
			}},
		}}, nil
	}
	var texts []string
	if err := json.Unmarshal(envelope.Input, &texts); err == nil {
		out := make([]schemas.EmbeddingInputItem, len(texts))
		for i := range texts {
			out[i] = schemas.EmbeddingInputItem{
				Content: schemas.EmbeddingContent{{
					Type: schemas.EmbeddingContentPartTypeText,
					Text: &texts[i],
				}},
			}
		}
		return out, nil
	}
	return nil, derrors.New(derrors.CodeInvalid, "router.parseEmbeddingInputItems", "input must be string or string array")
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
	bctx := c.bifrostContext(ctx, 0, false)
	resp, berr := c.bf.SpeechRequest(bctx, &schemas.BifrostSpeechRequest{
		Provider: provider,
		Model:    model,
		Input:    &schemas.SpeechInput{Input: req.Input},
		Params:   params,
	})
	if berr != nil {
		return nil, bifrostErr(berr, bifrostProviderHeader(bctx))
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
	bctx := c.bifrostContext(ctx, 0, false)
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
		return nil, "", bifrostErr(berr, bifrostProviderHeader(bctx))
	}
	if resp == nil {
		return nil, "", derrors.New(derrors.CodeUnavailable, "router.Transcribe", "empty transcription from backend")
	}
	if schemas.IsPlainTextTranscriptionFormat(resp.ResponseFormat) {
		return []byte(resp.Text), "text/plain; charset=utf-8", nil
	}
	body, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: resp.Text})
	if err != nil {
		return nil, "", derrors.Wrap(err, derrors.CodeInternal, "router.Transcribe", "encode json")
	}
	return body, "application/json", nil
}

func ensureSpeechDeadline(ctx context.Context, timeouts config.Timeouts) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	d := time.Duration(timeouts.SpeechSeconds()) * time.Second
	return context.WithTimeout(ctx, d)
}

func bifrostProviderHeader(bctx *schemas.BifrostContext) http.Header {
	if bctx == nil {
		return nil
	}
	raw, ok := bctx.Value(schemas.BifrostContextKeyProviderResponseHeaders).(map[string]string)
	if !ok || len(raw) == 0 {
		return nil
	}
	h := make(http.Header, len(raw))
	for k, v := range raw {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		h.Add(k, v)
	}
	return h
}

// chatBifrostErr maps a chat error from Bifrost. Anthropic backends get the fallback-route
// classification first; every other backend keeps bifrostErr.
func (c *Client) chatBifrostErr(backendID string, berr *schemas.BifrostError, header http.Header) error {
	if c != nil && c.reg != nil {
		if b, ok := c.reg.Backend(backendID); ok && b.IsAnthropicExtension() {
			if err := anthropicChatErr(berr); err != nil {
				return err
			}
		}
	}
	return bifrostErr(berr, header)
}

// anthropicChatErr classifies Claude refusals for fallback routes. Out of credit and
// overloaded become CodeNotReady (HTTP 503), which Switchyard treats as unavailable and
// falls back on at once. Any other 4xx except 429 keeps a caller-visible 4xx code, so it
// neither falls back nor trips the breaker. Returns nil to defer to bifrostErr.
func anthropicChatErr(berr *schemas.BifrostError) error {
	if berr == nil {
		return nil
	}
	status, errType, msg := 0, "", "request failed"
	if berr.StatusCode != nil {
		status = *berr.StatusCode
	}
	if berr.Error != nil {
		if berr.Error.Type != nil {
			errType = strings.TrimSpace(*berr.Error.Type)
		}
		if m := strings.TrimSpace(berr.Error.Message); m != "" {
			msg = m
		}
	}
	var code derrors.Code
	switch {
	case anthropicOutOfCredit(status, errType, msg), status == anthropicOverloadedStatus, errType == "overloaded_error":
		code = derrors.CodeNotReady
	case status == http.StatusTooManyRequests || status < 400 || status >= 500:
		return nil
	default:
		code = clientErrorCode(status)
	}
	out := derrors.New(code, "router.anthropic", msg).With("status", strconv.Itoa(status))
	if errType != "" {
		out = out.With("type", errType)
	}
	return out
}

// anthropicOverloadedStatus is Anthropic's non-standard overloaded status.
const anthropicOverloadedStatus = 529

// anthropicOutOfCredit reports a billing refusal: 402 billing_error, or a 400 whose
// message says the credit balance is too low.
func anthropicOutOfCredit(status int, errType, msg string) bool {
	if status == http.StatusPaymentRequired || errType == "billing_error" {
		return true
	}
	return status == http.StatusBadRequest && strings.Contains(strings.ToLower(msg), "credit balance")
}

// clientErrorCode maps an upstream 4xx to the closest domain code (HTTPStatus maps it back).
func clientErrorCode(status int) derrors.Code {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return derrors.CodeUnauthorized
	case http.StatusNotFound:
		return derrors.CodeNotFound
	case http.StatusConflict:
		return derrors.CodeConflict
	default:
		return derrors.CodeInvalid
	}
}

func bifrostErr(berr *schemas.BifrostError, header http.Header) error {
	if berr == nil {
		return derrors.New(derrors.CodeInternal, "router.bifrost", "bifrost error")
	}
	status := 0
	if berr.StatusCode != nil {
		status = *berr.StatusCode
	}
	msg := "request failed"
	if berr.Error != nil && strings.TrimSpace(berr.Error.Message) != "" {
		msg = strings.TrimSpace(berr.Error.Message)
	}
	hints := []string{msg}
	if berr.Error != nil && berr.Error.Code != nil {
		hints = append(hints, strings.TrimSpace(*berr.Error.Code))
	}
	if rl := cloudflare.ClassifyRateLimit("cloudflare.workers", status, header, bifrostRawBody(berr), hints...); rl != nil {
		return derrors.Wrap(rl, derrors.CodeRateLimited, "router.bifrost", "provider rate limited")
	}
	if berr.Error != nil && berr.Error.Error != nil {
		out := derrors.Wrap(berr.Error.Error, derrors.CodeUnavailable, "router.bifrost", "provider call")
		if status > 0 {
			out = out.With("status", fmt.Sprintf("%d", status))
		}
		return out
	}
	out := derrors.New(derrors.CodeUnavailable, "router.bifrost", msg)
	if status > 0 {
		out = out.With("status", fmt.Sprintf("%d", status))
	}
	return out
}

func bifrostRawBody(berr *schemas.BifrostError) []byte {
	if berr == nil {
		return nil
	}
	raw := berr.ExtraFields.RawResponse
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	case json.RawMessage:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return b
	}
}
