package router

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/behaviorengineering/polypus/internal/config"
)

// Client wraps Bifrost for Polypus multi-backend speech routing.
type Client struct {
	bf  *bifrost.Bifrost
	reg *Registry
}

// NewClient validates backends and initializes Bifrost.
func NewClient(cfg config.RouterConfig) (*Client, error) {
	reg, err := NewRegistry(cfg)
	if err != nil {
		return nil, err
	}
	account := NewAccount(cfg)
	bf, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account: account,
	})
	if err != nil {
		return nil, fmt.Errorf("bifrost init: %w", err)
	}
	return &Client{bf: bf, reg: reg}, nil
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
	provider, model, err := c.reg.ResolveTTS(req.Model)
	if err != nil {
		return nil, err
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
	bctx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)
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
	provider, model, err := c.reg.ResolveSTT(req.Model)
	if err != nil {
		return nil, "", err
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
	bctx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)
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
	if berr.Error != nil && berr.Error.Message != "" {
		return fmt.Errorf("%s", berr.Error.Message)
	}
	return fmt.Errorf("bifrost request failed")
}
