package cloudflare

import (
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/maximhq/bifrost/core/schemas"
)

const runSpeechPluginName = "polypus-cf-run-speech"

// BackendLookup resolves a Bifrost provider id to a backend definition.
type BackendLookup func(provider string) (config.BackendDef, bool)

// ClientGet constructs a Cloudflare extension client (defaults to GetClient).
type ClientGet func(config.BackendDef) (*Client, error)

// runSpeechPlugin short-circuits Bifrost speech/transcription for Cloudflare
// backends onto the extension /ai/run adapter (Workers AI has no /ai/v1/audio/*).
type runSpeechPlugin struct {
	lookup BackendLookup
	get    ClientGet
}

// NewRunSpeechPlugin returns a Bifrost LLMPlugin for CF /run speech.
// lookup must be non-nil. get defaults to GetClient when nil.
func NewRunSpeechPlugin(lookup BackendLookup, get ClientGet) schemas.LLMPlugin {
	if lookup == nil {
		panic("cloudflare: NewRunSpeechPlugin(nil BackendLookup)")
	}
	if get == nil {
		get = GetClient
	}
	return &runSpeechPlugin{lookup: lookup, get: get}
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

	switch req.RequestType {
	case schemas.SpeechStreamRequest, schemas.TranscriptionStreamRequest:
		provider, _, _ := req.GetRequestFields()
		if b, ok := p.cfBackend(provider); ok {
			return req, &schemas.LLMPluginShortCircuit{
				Error: runSpeechPluginErr(derrors.New(derrors.CodeInvalid, "cloudflare.runSpeech", "speech streaming not supported").
					With("backend", b.ID)),
			}, nil
		}
		return req, nil, nil
	case schemas.SpeechRequest:
		if req.SpeechRequest == nil {
			return req, nil, nil
		}
		b, ok := p.cfBackend(req.SpeechRequest.Provider)
		if !ok {
			return req, nil, nil
		}
		return req, p.shortCircuitSpeech(ctx, b, req.SpeechRequest), nil
	case schemas.TranscriptionRequest:
		if req.TranscriptionRequest == nil {
			return req, nil, nil
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

func (p *runSpeechPlugin) shortCircuitSpeech(
	ctx *schemas.BifrostContext,
	b config.BackendDef,
	sr *schemas.BifrostSpeechRequest,
) *schemas.LLMPluginShortCircuit {
	if _, ok := ctx.Deadline(); !ok {
		return &schemas.LLMPluginShortCircuit{
			Error: runSpeechPluginErr(derrors.New(derrors.CodeFailedPrecondition, "cloudflare.runSpeech", "context deadline required").
				With("backend", b.ID)),
		}
	}
	cf, err := p.get(b)
	if err != nil {
		return &schemas.LLMPluginShortCircuit{
			Error: runSpeechPluginErr(derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.runSpeech", "client").
				With("backend", b.ID)),
		}
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
		return &schemas.LLMPluginShortCircuit{
			Error: runSpeechPluginErr(derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.runSpeech", "synthesize").
				With("backend", b.ID)),
		}
	}
	if len(audio) == 0 {
		return &schemas.LLMPluginShortCircuit{
			Error: runSpeechPluginErr(derrors.New(derrors.CodeUnavailable, "cloudflare.runSpeech", "empty speech audio").
				With("backend", b.ID)),
		}
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
	if _, ok := ctx.Deadline(); !ok {
		return &schemas.LLMPluginShortCircuit{
			Error: runSpeechPluginErr(derrors.New(derrors.CodeFailedPrecondition, "cloudflare.runSpeech", "context deadline required").
				With("backend", b.ID)),
		}
	}
	cf, err := p.get(b)
	if err != nil {
		return &schemas.LLMPluginShortCircuit{
			Error: runSpeechPluginErr(derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.runSpeech", "client").
				With("backend", b.ID)),
		}
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
		return &schemas.LLMPluginShortCircuit{
			Error: runSpeechPluginErr(derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.runSpeech", "transcribe").
				With("backend", b.ID)),
		}
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

func runSpeechPluginErr(err error) *schemas.BifrostError {
	if err == nil {
		err = derrors.New(derrors.CodeInternal, "cloudflare.runSpeech", "unknown error")
	}
	falseVal := false
	code := string(derrors.CodeOf(err))
	return &schemas.BifrostError{
		IsBifrostError: true,
		AllowFallbacks: &falseVal,
		Error: &schemas.ErrorField{
			Message: err.Error(),
			Code:    &code,
			Error:   err,
		},
	}
}
