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
	defaultTimeoutEmbed        = 60 * time.Second
	defaultTimeoutSpeech       = 180 * time.Second
	defaultTimeoutChatCF       = 60 * time.Second
)

// BackendTimeouts overrides capability buckets for one backend id.
type BackendTimeouts struct {
	Chat         time.Duration
	ChatThinking time.Duration
	Vision       time.Duration
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
	Backends     map[string]backendTOFile `yaml:"backends"`
}

type backendTOFile struct {
	Chat         string `yaml:"chat"`
	ChatThinking string `yaml:"chat_thinking"`
	Vision       string `yaml:"vision"`
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
	min := t.Min
	max := t.Max
	if min <= 0 {
		min = defaultTimeoutMin
	}
	if max < min {
		max = defaultTimeoutMax
	}
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}

// ResolveChat picks a hop timeout: optional header, else vision / thinking / backend chat / chat.
func (t Timeouts) ResolveChat(header, backendID string, vision, thinking bool) time.Duration {
	if raw := strings.TrimSpace(header); raw != "" {
		if d, err := ParseTimeoutValue(raw); err == nil {
			return t.Clamp(d)
		}
	}
	be := t.Backends[strings.TrimSpace(backendID)]
	switch {
	case vision:
		if be.Vision > 0 {
			return t.Clamp(be.Vision)
		}
		return t.Clamp(t.Vision)
	case thinking:
		if be.ChatThinking > 0 {
			return t.Clamp(be.ChatThinking)
		}
		return t.Clamp(t.ChatThinking)
	}
	if be.Chat > 0 {
		return t.Clamp(be.Chat)
	}
	return t.Clamp(t.Chat)
}

// ResolveEmbed returns the embeddings hop timeout (header or embed bucket).
func (t Timeouts) ResolveEmbed(header string) time.Duration {
	if raw := strings.TrimSpace(header); raw != "" {
		if d, err := ParseTimeoutValue(raw); err == nil {
			return t.Clamp(d)
		}
	}
	return t.Clamp(t.Embed)
}

// ResolveSpeech returns the TTS/STT hop timeout (header or speech bucket).
func (t Timeouts) ResolveSpeech(header string) time.Duration {
	if raw := strings.TrimSpace(header); raw != "" {
		if d, err := ParseTimeoutValue(raw); err == nil {
			return t.Clamp(d)
		}
	}
	return t.Clamp(t.Speech)
}

// SpeechSeconds is the Bifrost per-provider request timeout for speech-only configs.
func (t Timeouts) SpeechSeconds() int {
	d := t.Speech
	if d <= 0 {
		d = defaultTimeoutSpeech
	}
	sec := int(t.Clamp(d).Seconds())
	if sec < 1 {
		return 1
	}
	return sec
}

// ProviderSeconds is the Bifrost network timeout covering chat/embed/speech hops.
func (t Timeouts) ProviderSeconds() int {
	d := t.Max
	if d <= 0 {
		d = defaultTimeoutMax
	}
	sec := int(t.Clamp(d).Seconds())
	if sec < 1 {
		return 1
	}
	return sec
}
