package cloudflare

import (
	"context"
	"log/slog"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/trace"
)

const (
	runSpeechPluginName = "polypus-cf-run-speech"
	opRunSpeech         = "cloudflare.runSpeech"
)

// Request kinds the plugin logs.
const (
	kindSpeech        = "speech"
	kindTranscription = "transcription"
)

// BackendLookup resolves a Bifrost provider id to a backend definition.
type BackendLookup func(provider string) (config.BackendDef, bool)

// ClientGet returns the Cloudflare extension client for a backend (Clients.Get in
// production).
type ClientGet func(config.BackendDef) (*Client, error)

// runSpeechPlugin short-circuits Bifrost speech/transcription for Cloudflare
// backends onto the extension /ai/run adapter (Workers AI has no /ai/v1/audio/*).
// speechCall is the backend, model and request kind one short circuit serves.
type speechCall struct {
	backend config.BackendDef
	model   string
	kind    string
}

type runSpeechPlugin struct {
	lookup BackendLookup
	get    ClientGet
	log    *slog.Logger
}

// NewRunSpeechPlugin returns a Bifrost LLMPlugin for CF /run speech. Panics when
// lookup, get or log is nil.
func NewRunSpeechPlugin(lookup BackendLookup, get ClientGet, log *slog.Logger) schemas.LLMPlugin {
	if lookup == nil || get == nil || log == nil {
		panic("cloudflare: NewRunSpeechPlugin(nil BackendLookup, ClientGet or logger)")
	}
	return &runSpeechPlugin{lookup: lookup, get: get, log: log}
}

func (p *runSpeechPlugin) GetName() string { return runSpeechPluginName }

func (p *runSpeechPlugin) Cleanup() error { return nil }

func (p *runSpeechPlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}

func (p *runSpeechPlugin) PostLLMHook(
	_ *schemas.BifrostContext,
	resp *schemas.BifrostResponse,
	bifrostErr *schemas.BifrostError,
) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bifrostErr, nil
}

func (p *runSpeechPlugin) PreLLMHook(
	ctx *schemas.BifrostContext,
	req *schemas.BifrostRequest,
) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if p == nil || p.lookup == nil || req == nil {
		return req, nil, nil
	}
	if ctx == nil {
		err := derrors.New(derrors.CodeInvalid, opRunSpeech, "context required")
		return req, &schemas.LLMPluginShortCircuit{Error: bifrostSpeechErr(err, true)}, nil
	}

	//exhaustive:ignore // the plugin acts on speech and transcription only; every other request type passes through
	switch req.RequestType {
	case schemas.SpeechStreamRequest, schemas.TranscriptionStreamRequest:
		provider, model, _ := req.GetRequestFields()
		if b, ok := p.cfBackend(provider); ok {
			err := derrors.New(derrors.CodeInvalid, opRunSpeech, "speech streaming not supported").
				With("backend", b.ID).
				With("model", model)
			return req, p.shortCircuitFail(ctx, speechCall{b, model, string(req.RequestType)}, err, true), nil
		}
		return req, nil, nil
	case schemas.SpeechRequest:
		if req.SpeechRequest == nil {
			err := derrors.New(derrors.CodeInvalid, opRunSpeech, "nil speech request payload")
			return req, p.shortCircuitFail(ctx, speechCall{backend: config.BackendDef{ID: "unknown"}, kind: kindSpeech}, err, true), nil
		}
		b, ok := p.cfBackend(req.SpeechRequest.Provider)
		if !ok {
			return req, nil, nil
		}
		return req, p.shortCircuitSpeech(ctx, b, req.SpeechRequest), nil
	case schemas.TranscriptionRequest:
		if req.TranscriptionRequest == nil {
			err := derrors.New(derrors.CodeInvalid, opRunSpeech, "nil transcription request payload")
			return req, p.shortCircuitFail(ctx, speechCall{backend: config.BackendDef{ID: "unknown"}, kind: kindTranscription}, err, true), nil
		}
		b, ok := p.cfBackend(req.TranscriptionRequest.Provider)
		if !ok {
			return req, nil, nil
		}
		return req, p.shortCircuitTranscription(ctx, b, req.TranscriptionRequest), nil
	default:
		return req, nil, nil
	}
}

func (p *runSpeechPlugin) cfBackend(provider schemas.ModelProvider) (config.BackendDef, bool) {
	b, ok := p.lookup(string(provider))
	if !ok || !b.IsCloudflareExtension() {
		return config.BackendDef{}, false
	}
	return b, true
}

// resolveClient checks the call has a deadline and gets the backend's client, or
// returns the short circuit that reports why it cannot run.
func (p *runSpeechPlugin) resolveClient(ctx *schemas.BifrostContext, call speechCall) (*Client, *schemas.LLMPluginShortCircuit) {
	if _, ok := ctx.Deadline(); !ok {
		err := derrors.New(derrors.CodeFailedPrecondition, opRunSpeech, "context deadline required").
			With("backend", call.backend.ID).
			With("model", call.model)
		return nil, p.shortCircuitFail(ctx, call, err, true)
	}
	cf, err := p.get(call.backend)
	if err != nil {
		wrapped := derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opRunSpeech, "client").
			With("backend", call.backend.ID).
			With("model", call.model)
		return nil, p.shortCircuitFail(ctx, call, wrapped, false)
	}
	if cf == nil {
		err := derrors.New(derrors.CodeInternal, opRunSpeech, "client nil").
			With("backend", call.backend.ID).
			With("model", call.model)
		return nil, p.shortCircuitFail(ctx, call, err, true)
	}
	return cf, nil
}

func (p *runSpeechPlugin) shortCircuitSpeech(
	ctx *schemas.BifrostContext,
	b config.BackendDef,
	sr *schemas.BifrostSpeechRequest,
) *schemas.LLMPluginShortCircuit {
	if sr == nil {
		err := derrors.New(derrors.CodeInvalid, opRunSpeech, "speech request required").With("backend", b.ID)
		return p.shortCircuitFail(ctx, speechCall{backend: b, kind: kindSpeech}, err, true)
	}
	model := sr.Model
	call := speechCall{backend: b, model: model, kind: kindSpeech}
	cf, fail := p.resolveClient(ctx, call)
	if fail != nil {
		return fail
	}
	input := ""
	if sr.Input != nil {
		input = sr.Input.Input
	}
	voice := ""
	format := ""
	if sr.Params != nil {
		format = strings.TrimSpace(sr.Params.ResponseFormat)
		if sr.Params.VoiceConfig != nil && sr.Params.VoiceConfig.Voice != nil {
			voice = strings.TrimSpace(*sr.Params.VoiceConfig.Voice)
		}
	}
	audio, _, err := cf.Synthesize(ctx, SpeechRequest{
		Model:          sr.Model,
		Input:          input,
		Voice:          voice,
		ResponseFormat: format,
	})
	if err != nil {
		wrapped := derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opRunSpeech, "synthesize").
			With("backend", b.ID).
			With("model", model)
		return p.shortCircuitFail(ctx, call, wrapped, false)
	}
	if len(audio) == 0 {
		err := derrors.New(derrors.CodeUnavailable, opRunSpeech, "empty speech audio").
			With("backend", b.ID).
			With("model", model)
		return p.shortCircuitFail(ctx, call, err, false)
	}
	return &schemas.LLMPluginShortCircuit{
		Response: &schemas.BifrostResponse{
			SpeechResponse: &schemas.BifrostSpeechResponse{Audio: audio},
		},
	}
}

func (p *runSpeechPlugin) shortCircuitTranscription(
	ctx *schemas.BifrostContext,
	b config.BackendDef,
	tr *schemas.BifrostTranscriptionRequest,
) *schemas.LLMPluginShortCircuit {
	if tr == nil {
		err := derrors.New(derrors.CodeInvalid, opRunSpeech, "transcription request required").With("backend", b.ID)
		return p.shortCircuitFail(ctx, speechCall{backend: b, kind: kindTranscription}, err, true)
	}
	model := tr.Model
	call := speechCall{backend: b, model: model, kind: kindTranscription}
	cf, fail := p.resolveClient(ctx, call)
	if fail != nil {
		return fail
	}
	var file []byte
	filename := ""
	if tr.Input != nil {
		file = tr.Input.File
		filename = tr.Input.Filename
	}
	lang := ""
	if tr.Params != nil && tr.Params.Language != nil {
		lang = strings.TrimSpace(*tr.Params.Language)
	}
	text, err := cf.Transcribe(ctx, TranscriptionRequest{
		Model:    tr.Model,
		Audio:    file,
		Filename: filename,
		Language: lang,
	})
	if err != nil {
		wrapped := derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opRunSpeech, "transcribe").
			With("backend", b.ID).
			With("model", model)
		return p.shortCircuitFail(ctx, call, wrapped, false)
	}
	if strings.TrimSpace(text) == "" {
		err := derrors.New(derrors.CodeUnavailable, opRunSpeech, "empty transcript").
			With("backend", b.ID).
			With("model", model)
		return p.shortCircuitFail(ctx, call, err, false)
	}
	resp := &schemas.BifrostTranscriptionResponse{Text: text}
	if tr.Params != nil && tr.Params.ResponseFormat != nil {
		resp.ResponseFormat = tr.Params.ResponseFormat
	}
	return &schemas.LLMPluginShortCircuit{
		Response: &schemas.BifrostResponse{
			TranscriptionResponse: resp,
		},
	}
}

func (p *runSpeechPlugin) shortCircuitFail(ctx context.Context, call speechCall, err error, isBifrost bool) *schemas.LLMPluginShortCircuit {
	attrs := []any{
		"backend", call.backend.ID,
		"model", call.model,
		"request_type", call.kind,
		"err", err,
	}
	if tid := traceIDFromCtx(ctx); tid != "" {
		attrs = append([]any{"trace_id", tid}, attrs...)
	}
	p.log.Error("polypus: cf speech short-circuit failed", attrs...)
	return &schemas.LLMPluginShortCircuit{Error: bifrostSpeechErr(err, isBifrost)}
}

func bifrostSpeechErr(err error, isBifrost bool) *schemas.BifrostError {
	if err == nil {
		err = derrors.New(derrors.CodeInternal, opRunSpeech, "unknown error")
	}
	falseVal := false
	code := string(derrors.CodeOf(err))
	return &schemas.BifrostError{
		IsBifrostError: isBifrost,
		AllowFallbacks: &falseVal,
		Error: &schemas.ErrorField{
			Message: err.Error(),
			Code:    &code,
			Error:   err,
		},
	}
}

func traceIDFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}
