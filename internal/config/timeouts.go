package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TimeoutHeader is the optional client override (Go duration or integer seconds).
const TimeoutHeader = "X-Polypus-Timeout"

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

func parseTimeoutsFile(file timeoutsFile) (Timeouts, error) {
	out := DefaultTimeouts()
	var err error
	if out.Min, err = overlayDuration(file.Min, out.Min); err != nil {
		return Timeouts{}, fmt.Errorf("timeouts.min: %w", err)
	}
	if out.Max, err = overlayDuration(file.Max, out.Max); err != nil {
		return Timeouts{}, fmt.Errorf("timeouts.max: %w", err)
	}
	if out.Chat, err = overlayDuration(file.Chat, out.Chat); err != nil {
		return Timeouts{}, fmt.Errorf("timeouts.chat: %w", err)
	}
	if out.ChatThinking, err = overlayDuration(file.ChatThinking, out.ChatThinking); err != nil {
		return Timeouts{}, fmt.Errorf("timeouts.chat_thinking: %w", err)
	}
	if out.Vision, err = overlayDuration(file.Vision, out.Vision); err != nil {
		return Timeouts{}, fmt.Errorf("timeouts.vision: %w", err)
	}
	if out.Embed, err = overlayDuration(file.Embed, out.Embed); err != nil {
		return Timeouts{}, fmt.Errorf("timeouts.embed: %w", err)
	}
	if out.Speech, err = overlayDuration(file.Speech, out.Speech); err != nil {
		return Timeouts{}, fmt.Errorf("timeouts.speech: %w", err)
	}
	if out.StreamIdle, err = overlayDuration(file.StreamIdle, out.StreamIdle); err != nil {
		return Timeouts{}, fmt.Errorf("timeouts.stream_idle: %w", err)
	}
	if len(file.Backends) > 0 {
		if out.Backends == nil {
			out.Backends = map[string]BackendTimeouts{}
		}
		for id, entry := range file.Backends {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			cur := out.Backends[id]
			if cur.Chat, err = overlayDuration(entry.Chat, cur.Chat); err != nil {
				return Timeouts{}, fmt.Errorf("timeouts.backends.%s.chat: %w", id, err)
			}
			if cur.ChatThinking, err = overlayDuration(entry.ChatThinking, cur.ChatThinking); err != nil {
				return Timeouts{}, fmt.Errorf("timeouts.backends.%s.chat_thinking: %w", id, err)
			}
			if cur.Vision, err = overlayDuration(entry.Vision, cur.Vision); err != nil {
				return Timeouts{}, fmt.Errorf("timeouts.backends.%s.vision: %w", id, err)
			}
			if cur.StreamIdle, err = overlayDuration(entry.StreamIdle, cur.StreamIdle); err != nil {
				return Timeouts{}, fmt.Errorf("timeouts.backends.%s.stream_idle: %w", id, err)
			}
			out.Backends[id] = cur
		}
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
