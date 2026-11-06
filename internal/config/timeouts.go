package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TimeoutHeader is the optional client override (Go duration or integer seconds).
const TimeoutHeader = "X-Polypus-Timeout"

// InterventionHeader labels Polypus-owned rewrites or refuses on the response.
const InterventionHeader = "X-Polypus-Intervention"

const (
	defaultTimeoutMin          = 5 * time.Second
	defaultTimeoutMax          = 900 * time.Second
	defaultTimeoutChat         = 120 * time.Second
	defaultTimeoutChatThinking = 600 * time.Second
	defaultTimeoutVision       = 300 * time.Second
	defaultTimeoutEmbed        = 15 * time.Second
	defaultTimeoutSpeech       = 180 * time.Second
	defaultTimeoutChatCF       = 60 * time.Second
	// defaultStreamIdle is the longest gap allowed between chunks of a chat stream.
	// Generous, so a long thinking pause is not cut off; Anthropic pings reset it.
	defaultStreamIdle = 5 * time.Minute
)

// BackendTimeouts overrides capability buckets for one backend id.
type BackendTimeouts struct {
	Chat         time.Duration
	ChatThinking time.Duration
	Vision       time.Duration
	StreamIdle   time.Duration
}

// Timeouts are static per-hop budgets. Header overrides are clamped to Min..Max.
type Timeouts struct {
	Min          time.Duration
	Max          time.Duration
	Chat         time.Duration
	ChatThinking time.Duration
	Vision       time.Duration
	Embed        time.Duration
	Speech       time.Duration
	StreamIdle   time.Duration
	Backends     map[string]BackendTimeouts
}

type timeoutsFile struct {
	Min          string                   `yaml:"min"`
	Max          string                   `yaml:"max"`
	Chat         string                   `yaml:"chat"`
	ChatThinking string                   `yaml:"chat_thinking"`
	Vision       string                   `yaml:"vision"`
	Embed        string                   `yaml:"embed"`
	Speech       string                   `yaml:"speech"`
	StreamIdle   string                   `yaml:"stream_idle"`
	Backends     map[string]backendTOFile `yaml:"backends"`
}

type backendTOFile struct {
	Chat         string `yaml:"chat"`
	ChatThinking string `yaml:"chat_thinking"`
	Vision       string `yaml:"vision"`
	StreamIdle   string `yaml:"stream_idle"`
}

// DefaultTimeouts returns hang ceilings for chat (thinking off), vision, embed, and speech.
func DefaultTimeouts() Timeouts {
	return Timeouts{
		Min:          defaultTimeoutMin,
		Max:          defaultTimeoutMax,
		Chat:         defaultTimeoutChat,
		ChatThinking: defaultTimeoutChatThinking,
		Vision:       defaultTimeoutVision,
		Embed:        defaultTimeoutEmbed,
		Speech:       defaultTimeoutSpeech,
		StreamIdle:   defaultStreamIdle,
		Backends: map[string]BackendTimeouts{
			"cf_local": {Chat: defaultTimeoutChatCF},
		},
	}
}

// durationField is one YAML duration and the Timeouts field it overlays.
type durationField struct {
	key string
	raw string
	dst *time.Duration
}

// overlayDurations overlays each raw value onto its field; prefix names the
// YAML block in errors.
func overlayDurations(prefix string, fields []durationField) error {
	for _, f := range fields {
		d, err := overlayDuration(f.raw, *f.dst)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", prefix, f.key, err)
		}
		*f.dst = d
	}
	return nil
}

func parseTimeoutsFile(file timeoutsFile) (Timeouts, error) {
	out := DefaultTimeouts()
	if err := overlayDurations("timeouts", []durationField{
		{"min", file.Min, &out.Min},
		{"max", file.Max, &out.Max},
		{"chat", file.Chat, &out.Chat},
		{"chat_thinking", file.ChatThinking, &out.ChatThinking},
		{"vision", file.Vision, &out.Vision},
		{"embed", file.Embed, &out.Embed},
		{"speech", file.Speech, &out.Speech},
		{"stream_idle", file.StreamIdle, &out.StreamIdle},
	}); err != nil {
		return Timeouts{}, err
	}
	if len(file.Backends) > 0 && out.Backends == nil {
		out.Backends = map[string]BackendTimeouts{}
	}
	for id, entry := range file.Backends {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		cur := out.Backends[id]
		if err := overlayDurations("timeouts.backends."+id, []durationField{
			{"chat", entry.Chat, &cur.Chat},
			{"chat_thinking", entry.ChatThinking, &cur.ChatThinking},
			{"vision", entry.Vision, &cur.Vision},
			{"stream_idle", entry.StreamIdle, &cur.StreamIdle},
		}); err != nil {
			return Timeouts{}, err
		}
		out.Backends[id] = cur
	}
	if out.Min <= 0 || out.Max < out.Min {
		return Timeouts{}, fmt.Errorf("timeouts: max must be >= min")
	}
	return out, nil
}

func overlayDuration(raw string, cur time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cur, nil
	}
	d, err := ParseTimeoutValue(raw)
	if err != nil {
		return 0, err
	}
	return d, nil
}

// ParseTimeoutValue accepts a Go duration ("120s") or integer seconds ("120").
func ParseTimeoutValue(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty timeout")
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("timeout must be positive")
		}
		return d, nil
	}
	sec, err := strconv.Atoi(raw)
	if err != nil || sec <= 0 {
		return 0, fmt.Errorf("invalid timeout %q", raw)
	}
	return time.Duration(sec) * time.Second, nil
}

// Clamp bounds d to Min..Max.
func (t Timeouts) Clamp(d time.Duration) time.Duration {
	lo := t.Min
	hi := t.Max
	if lo <= 0 {
		lo = defaultTimeoutMin
	}
	if hi < lo {
		hi = defaultTimeoutMax
	}
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

// ResolveChat picks a hop timeout: optional header, else vision / thinking / backend chat / chat.
func (t Timeouts) ResolveChat(header, backendID string, kind ChatKind) time.Duration {
	be := t.Backends[strings.TrimSpace(backendID)]
	switch kind {
	case ChatVision:
		return t.headerOr(header, firstPositive(be.Vision, t.Vision))
	case ChatThinking:
		return t.headerOr(header, firstPositive(be.ChatThinking, t.ChatThinking))
	case ChatPlain:
		return t.headerOr(header, firstPositive(be.Chat, t.Chat))
	default:
		return t.headerOr(header, firstPositive(be.Chat, t.Chat))
	}
}

// ChatKind picks which chat budget a request gets.
type ChatKind int

// Chat budgets.
const (
	ChatPlain ChatKind = iota
	ChatVision
	ChatThinking
)

// ChatKindOf is the budget for a chat request; vision wins over thinking.
func ChatKindOf(vision, thinking bool) ChatKind {
	switch {
	case vision:
		return ChatVision
	case thinking:
		return ChatThinking
	default:
		return ChatPlain
	}
}

// headerOr is the caller's timeout header when it parses, else fallback, clamped.
func (t Timeouts) headerOr(header string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(header); raw != "" {
		if d, err := ParseTimeoutValue(raw); err == nil {
			return t.Clamp(d)
		}
	}
	return t.Clamp(fallback)
}

func firstPositive(ds ...time.Duration) time.Duration {
	for _, d := range ds {
		if d > 0 {
			return d
		}
	}
	return 0
}

// ResolveStreamIdle returns the longest gap allowed between chunks of a chat stream
// for backendID: its own stream_idle, else the global one, else the default.
func (t Timeouts) ResolveStreamIdle(backendID string) time.Duration {
	if be := t.Backends[strings.TrimSpace(backendID)]; be.StreamIdle > 0 {
		return be.StreamIdle
	}
	if t.StreamIdle > 0 {
		return t.StreamIdle
	}
	return defaultStreamIdle
}

// ResolveEmbed returns the embeddings hop timeout (header or embed bucket).
func (t Timeouts) ResolveEmbed(header string) time.Duration {
	return t.headerOr(header, t.Embed)
}

// ResolveSpeech returns the TTS/STT hop timeout (header or speech bucket).
func (t Timeouts) ResolveSpeech(header string) time.Duration {
	return t.headerOr(header, t.Speech)
}

// SpeechSeconds is the Bifrost per-provider request timeout for speech-only configs.
func (t Timeouts) SpeechSeconds() int {
	return t.seconds(t.Speech, defaultTimeoutSpeech)
}

// ProviderSeconds is the Bifrost network timeout covering chat/embed/speech hops.
func (t Timeouts) ProviderSeconds() int {
	return t.seconds(t.Max, defaultTimeoutMax)
}

// seconds is d (or def when d is unset), clamped, in whole seconds of at least one.
func (t Timeouts) seconds(d, def time.Duration) int {
	if d <= 0 {
		d = def
	}
	return max(1, int(t.Clamp(d).Seconds()))
}
