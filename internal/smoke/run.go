// Package smoke runs multi-channel L1 probes against a Polypus gateway.
package smoke

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
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
	Channels       []string // empty means all
	ChatModel      string
	TTSModel       string
	STTModel       string
	SystemOneModel string
	BatchModel     string
	TTSVoice       string
	TTSText        string
	RequireCF      bool // when true, TypeSafe billing skips become failures (CI)
	Temperature    float64
	MaxTokens      int
}

// Result is one probe outcome (JSONL-friendly).
type Result struct {
	Channel string `json:"channel"`
	Model   string `json:"model,omitempty"`
	Probe   string `json:"probe"`
	Status  string `json:"status"` // pass | fail | skip
	Ms      int64  `json:"ms"`
	Detail  string `json:"detail"`
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
	opts.Normalize()

	var out []Result
	var failed bool
	for _, ch := range opts.Channels {
		ch = strings.ToLower(strings.TrimSpace(ch))
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
			results = []Result{{Channel: ch, Probe: "unknown", Status: "fail", Detail: "unknown channel"}}
			err = fmt.Errorf("unknown channel %q", ch)
		}
		out = append(out, results...)
		if err != nil {
			failed = true
		}
		for _, r := range results {
			if r.Status == "fail" {
				failed = true
			}
		}
	}
	if failed {
		return out, fmt.Errorf("smoke: one or more channels failed")
	}
	return out, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func timed(channel, probe, model string, fn func() (string, error)) Result {
	start := time.Now()
	row := Result{Channel: channel, Probe: probe, Model: model, Status: "pass"}
	detail, err := fn()
	row.Ms = time.Since(start).Milliseconds()
	if err != nil {
		row.Status = "fail"
		row.Detail = err.Error()
		return row
	}
	row.Detail = detail
	return row
}
