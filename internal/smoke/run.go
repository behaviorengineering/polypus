// Package smoke runs multi-channel L1 probes against a Polypus gateway.
package smoke

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"go.opentelemetry.io/otel/codes"
)

// Channel names accepted by Run / Options.Channels.
const (
	ChannelChat      = "chat"
	ChannelTTS       = "tts"
	ChannelSTT       = "stt"
	ChannelSystemOne = "systemone"
	ChannelBatch     = "batch"
)

// Default cheap Cloudflare models for CI and smoke-all.
const (
	DefaultChatModel      = "cf_local/@cf/ibm-granite/granite-4.0-h-micro"
	DefaultTTSModel       = "cf_local/@cf/deepgram/aura-2-en"
	DefaultSTTModel       = "cf_local/@cf/deepgram/nova-3"
	DefaultSystemOneModel = "cf_local/typesafe/jev"
	// DefaultBatchModel must be in Cloudflare Workers AI async batch allow-list.
	DefaultBatchModel = "cf_local/@cf/google/gemma-4-26b-a4b-it"
	DefaultTTSVoice   = "luna"
)

// Options configures a multi-channel smoke run.
type Options struct {
	BaseURL        string
	Channels       []string // empty means all.
	ChatModel      string
	TTSModel       string
	STTModel       string
	SystemOneModel string
	BatchModel     string
	TTSVoice       string
	TTSText        string
	AudioOutPath   string // optional TTS output file (POLYPUS_SMOKE_OUT).
	RequireCF      bool   // when true, TypeSafe billing skips become failures (CI).
	// RequireSelectedModel fails chat when Switchyard routing metadata is missing.
	RequireSelectedModel bool
	// AllowedSelectedModels when non-empty requires chat selected leaf ∈ list.
	AllowedSelectedModels []string
	Temperature           float64
	MaxTokens             int
}

// Status is a probe outcome.
type Status string

// Probe outcomes.
const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// Result is one probe outcome (JSONL-friendly).
type Result struct {
	Channel       string `json:"channel"`
	Model         string `json:"model,omitempty"`
	SelectedModel string `json:"selected_model,omitempty"`
	Probe         string `json:"probe"`
	Status        Status `json:"status"`
	Ms            int64  `json:"ms"`
	Detail        string `json:"detail"`
	// cause is the failed probe's error, for classifying it (Detail is its text).
	cause error
}

// AllChannels is the default CI / smoke-all set.
func AllChannels() []string {
	return []string{ChannelChat, ChannelTTS, ChannelSTT, ChannelSystemOne}
}

// Normalize fills defaults on Options.
func (o *Options) Normalize() {
	if o == nil {
		return
	}
	o.BaseURL = strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if o.BaseURL == "" {
		o.BaseURL = envOr("POLYPUS_BASE_URL", "http://127.0.0.1:1320")
	}
	if len(o.Channels) == 0 {
		o.Channels = AllChannels()
	}
	if strings.TrimSpace(o.ChatModel) == "" {
		o.ChatModel = envOr("POLYPUS_CHAT_SMOKE_MODEL", DefaultChatModel)
	}
	if strings.TrimSpace(o.TTSModel) == "" {
		o.TTSModel = envOr("POLYPUS_DEFAULT_MODEL", DefaultTTSModel)
	}
	if strings.TrimSpace(o.STTModel) == "" {
		o.STTModel = envOr("POLYPUS_DEFAULT_STT_MODEL", DefaultSTTModel)
	}
	if strings.TrimSpace(o.SystemOneModel) == "" {
		o.SystemOneModel = envOr("POLYPUS_SYSTEMONE_MODEL", DefaultSystemOneModel)
	}
	if strings.TrimSpace(o.BatchModel) == "" {
		o.BatchModel = envOr("POLYPUS_BATCH_SMOKE_MODEL", DefaultBatchModel)
	}
	if strings.TrimSpace(o.TTSVoice) == "" {
		o.TTSVoice = envOr("POLYPUS_DEFAULT_VOICE", DefaultTTSVoice)
	}
	if strings.TrimSpace(o.TTSText) == "" {
		o.TTSText = envOr("POLYPUS_SMOKE_TEXT", "Here is what the file shows for this episode.")
	}
	if strings.TrimSpace(o.AudioOutPath) == "" {
		o.AudioOutPath = strings.TrimSpace(os.Getenv("POLYPUS_SMOKE_OUT"))
	}
	if o.Temperature == 0 {
		o.Temperature = 0.1
	}
	if o.MaxTokens == 0 {
		o.MaxTokens = 256
	}
}

// Run executes configured channels. Returns non-nil error if any channel fails.
// Skip is for TypeSafe billing locally when RequireCF is false. Credentials live
// on the gateway (config secrets + keyring or env), not in the smoke CLI process.
func Run(ctx context.Context, opts Options) ([]Result, error) {
	if ctx == nil {
		return nil, derrors.New(derrors.CodeInvalid, "smoke.Run", "context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "smoke.Run", "context deadline required")
	}
	opts.Normalize()

	var out []Result
	var errs []error
	for _, ch := range opts.Channels {
		ch = strings.ToLower(strings.TrimSpace(ch))
		results, err := runChannel(ctx, ch, opts)
		out = append(out, results...)
		if err != nil {
			errs = append(errs, fmt.Errorf("channel %s: %w", ch, err))
			continue
		}
		for _, r := range results {
			if r.Status == StatusFail {
				errs = append(errs, fmt.Errorf("channel %s: probe %s: %s", ch, r.Probe, r.Detail))
			}
		}
	}
	if len(errs) > 0 {
		return out, derrors.Wrap(errors.Join(errs...), derrors.CodeUnavailable, "smoke.Run", "one or more channels failed")
	}
	return out, nil
}

// runChannel runs one channel under its own span, so the probes' client spans
// share a parent and the gateway's server spans join the same trace.
func runChannel(ctx context.Context, ch string, opts Options) ([]Result, error) {
	ctx, span := observability.Tracer().Start(ctx, "polypus-smoke."+ch)
	defer span.End()
	var results []Result
	var err error
	switch ch {
	case ChannelChat:
		results, err = runChat(ctx, opts)
	case ChannelTTS:
		results, err = runTTS(ctx, opts)
	case ChannelSTT:
		results, err = runSTT(ctx, opts)
	case ChannelSystemOne:
		results, err = runSystemOne(ctx, opts)
	case ChannelBatch:
		results, err = runBatch(ctx, opts)
	default:
		results = []Result{{Channel: ch, Probe: "unknown", Status: StatusFail, Detail: "unknown channel"}}
		err = fmt.Errorf("unknown channel %q", ch)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return results, err
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func timed(channel, probe, model string, fn func() (string, error)) Result {
	start := time.Now()
	row := Result{Channel: channel, Probe: probe, Model: model, Status: StatusPass}
	detail, err := fn()
	row.Ms = time.Since(start).Milliseconds()
	if err != nil {
		row.Status = StatusFail
		row.Detail = err.Error()
		row.cause = err
		return row
	}
	row.Detail = detail
	return row
}
