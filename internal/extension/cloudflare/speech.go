package cloudflare

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

const speechTimeout = 300 * time.Second

// doSpeech runs /run over the shared speech client. If ctx has no deadline,
// a fallback speechTimeout is applied so calls cannot hang forever.
func (c *Client) doSpeech(ctx context.Context, req *http.Request) (*http.Response, error) {
	if c == nil || c.speechClient == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "cloudflare.doSpeech", "not configured")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, speechTimeout)
		defer cancel()
		req = req.WithContext(ctx)
	}
	return c.speechClient.Do(req)
}

// SpeechRequest is OpenAI-compatible TTS input for the Cloudflare Workers AI path.
// Kept separate from router.SpeechRequest so CF-only fields can diverge without
// coupling the Bifrost speech surface.
type SpeechRequest struct {
	Model          string
	Input          string
	Voice          string
	ResponseFormat string
}

// TranscriptionRequest is OpenAI-compatible STT input.
type TranscriptionRequest struct {
	Model    string
	Audio    []byte
	Filename string
	Language string
}

// Synthesize calls Workers AI TTS (Deepgram Aura).
func (c *Client) Synthesize(ctx context.Context, req SpeechRequest) ([]byte, string, error) {
	if c == nil {
		return nil, "", derrors.New(derrors.CodeFailedPrecondition, "cloudflare.Synthesize", "not configured")
	}
	input := strings.TrimSpace(req.Input)
	if input == "" {
		return nil, "", derrors.New(derrors.CodeInvalid, "cloudflare.Synthesize", "empty input")
	}
	model := NormalizeModel(strings.TrimSpace(req.Model))
	if model == "" {
		model = defaultTTSModel()
	}
	speaker := firstNonEmpty(strings.TrimSpace(req.Voice), defaultVoice(), "luna")
	encoding := firstNonEmpty(strings.TrimSpace(req.ResponseFormat), "mp3")

	body := map[string]string{
		"text":     input,
		"speaker":  speaker,
		"encoding": encoding,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, "", derrors.Wrap(err, derrors.CodeInternal, "cloudflare.Synthesize", "marshal")
	}

	target := runURL(c.apiBase, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, "", derrors.Wrap(err, derrors.CodeInternal, "cloudflare.Synthesize", "request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "audio/mpeg,application/octet-stream,application/json")

	resp, err := c.doSpeech(ctx, httpReq)
	if err != nil {
		return nil, "", derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.Synthesize", "post /run").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()

	audio, contentType, err := readAudioBody(resp)
	if err != nil {
		return nil, "", err
	}
	if len(audio) == 0 {
		return nil, "", derrors.New(derrors.CodeUnavailable, "cloudflare.Synthesize", "empty audio").
			With("target", target)
	}
	return audio, contentType, nil
}

// Transcribe calls Workers AI STT (Deepgram Nova-3).
func (c *Client) Transcribe(ctx context.Context, req TranscriptionRequest) (string, error) {
	if c == nil {
		return "", derrors.New(derrors.CodeFailedPrecondition, "cloudflare.Transcribe", "not configured")
	}
	if len(req.Audio) == 0 {
		return "", derrors.New(derrors.CodeInvalid, "cloudflare.Transcribe", "empty audio")
	}
	model := NormalizeModel(strings.TrimSpace(req.Model))
	if model == "" {
		model = defaultSTTModel()
	}

	mime := mimeFromFilename(req.Filename)
	if mime == "" {
		mime = "audio/mpeg"
	}

	q := url.Values{}
	lang := strings.TrimSpace(req.Language)
	if lang == "" {
		lang = "en-AU"
	}
	q.Set("language", lang)

	target := runURL(c.apiBase, model)
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(req.Audio))
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, "cloudflare.Transcribe", "request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", mime)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.doSpeech(ctx, httpReq)
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.Transcribe", "post /run").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.Transcribe", "read body")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", derrors.New(derrors.CodeUnavailable, "cloudflare.Transcribe", "workers ai error").
			With("status", strconv.Itoa(resp.StatusCode)).
			With("body", truncate(string(body), 256))
	}
	return parseTranscript(body)
}

func readAudioBody(resp *http.Response) ([]byte, string, error) {
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, contentType, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.speech", "read body")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, contentType, derrors.New(derrors.CodeUnavailable, "cloudflare.speech", "workers ai error").
			With("status", strconv.Itoa(resp.StatusCode)).
			With("body", truncate(string(body), 256))
	}
	if strings.HasPrefix(strings.ToLower(contentType), "application/json") || looksLikeJSON(body) {
		var envelope struct {
			Success bool `json:"success"`
			Errors  []struct {
				Message string `json:"message"`
			} `json:"errors"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, contentType, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.speech", "parse json")
		}
		if !envelope.Success {
			msg := "cloudflare workers ai error"
			if len(envelope.Errors) > 0 && envelope.Errors[0].Message != "" {
				msg = envelope.Errors[0].Message
			}
			return nil, contentType, derrors.New(derrors.CodeUnavailable, "cloudflare.speech", msg)
		}
		if len(envelope.Result) > 0 {
			if envelope.Result[0] == '"' {
				var audioStr string
				if err := json.Unmarshal(envelope.Result, &audioStr); err != nil {
					return nil, contentType, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.speech", "parse result string")
				}
				if dec, err := base64.StdEncoding.DecodeString(audioStr); err == nil && len(dec) > 0 {
					return dec, contentType, nil
				}
				return []byte(audioStr), contentType, nil
			}
			return envelope.Result, contentType, nil
		}
	}
	return body, contentType, nil
}

func looksLikeJSON(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && (b[0] == '{' || b[0] == '[')
}

func parseTranscript(body []byte) (string, error) {
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Success && len(envelope.Result) > 0 {
		body = envelope.Result
	}

	var flat struct {
		Text       string `json:"text"`
		Transcript string `json:"transcript"`
	}
	if err := json.Unmarshal(body, &flat); err == nil {
		if t := strings.TrimSpace(flat.Text); t != "" {
			return t, nil
		}
		if t := strings.TrimSpace(flat.Transcript); t != "" {
			return t, nil
		}
	}

	var deepgram struct {
		Results struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string `json:"transcript"`
				} `json:"alternatives"`
			} `json:"channels"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &deepgram); err == nil {
		for _, ch := range deepgram.Results.Channels {
			for _, alt := range ch.Alternatives {
				if t := strings.TrimSpace(alt.Transcript); t != "" {
					return t, nil
				}
			}
		}
	}

	return "", derrors.New(derrors.CodeUnavailable, "cloudflare.Transcribe", "no transcript in response")
}

func mimeFromFilename(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(lower, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(lower, ".m4a"):
		return "audio/mp4"
	case strings.HasSuffix(lower, ".webm"):
		return "audio/webm"
	default:
		return ""
	}
}

func defaultTTSModel() string {
	if m := strings.TrimSpace(os.Getenv("POLYPUS_CF_TTS_MODEL")); m != "" {
		return NormalizeModel(m)
	}
	return "@cf/deepgram/aura-2-en"
}

func defaultSTTModel() string {
	if m := strings.TrimSpace(os.Getenv("POLYPUS_CF_STT_MODEL")); m != "" {
		return NormalizeModel(m)
	}
	return "@cf/deepgram/nova-3"
}

func defaultVoice() string {
	if v := strings.TrimSpace(os.Getenv("POLYPUS_CF_VOICE")); v != "" {
		return v
	}
	return "luna"
}
