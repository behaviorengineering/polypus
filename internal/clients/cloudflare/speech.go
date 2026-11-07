package cloudflare

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/clients/httpx"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
)

// Op names for the speech errors.
const (
	opSynthesize = "cloudflare.Synthesize"
	opTranscribe = "cloudflare.Transcribe"
	opSpeech     = "cloudflare.speech"
)

// Built-in speech defaults, used for any SpeechDefaults field left empty.
const (
	builtinTTSModel = "@cf/deepgram/aura-2-en"
	builtinSTTModel = "@cf/deepgram/nova-3"
	builtinVoice    = "luna"
	builtinLanguage = "en-AU"
)

// SpeechDefaults are what speech uses when a request leaves the model, voice or
// language empty. An empty field takes the built-in default.
type SpeechDefaults struct {
	TTSModel string
	STTModel string
	Voice    string
	Language string
}

// SpeechDefaultsFromEnv reads POLYPUS_CF_TTS_MODEL, POLYPUS_CF_STT_MODEL and
// POLYPUS_CF_VOICE. A composition root calls it once and passes the result in through
// Clients.Speech; unset variables stay empty, and the language keeps its built-in.
func SpeechDefaultsFromEnv() SpeechDefaults {
	return SpeechDefaults{
		TTSModel: strings.TrimSpace(os.Getenv("POLYPUS_CF_TTS_MODEL")),
		STTModel: strings.TrimSpace(os.Getenv("POLYPUS_CF_STT_MODEL")),
		Voice:    strings.TrimSpace(os.Getenv("POLYPUS_CF_VOICE")),
	}
}

// withBuiltins fills every empty field with its built-in default and normalises the
// models.
func (d SpeechDefaults) withBuiltins() SpeechDefaults {
	return SpeechDefaults{
		TTSModel: NormalizeModel(catalog.FirstNonEmpty(d.TTSModel, builtinTTSModel)),
		STTModel: NormalizeModel(catalog.FirstNonEmpty(d.STTModel, builtinSTTModel)),
		Voice:    catalog.FirstNonEmpty(d.Voice, builtinVoice),
		Language: catalog.FirstNonEmpty(d.Language, builtinLanguage),
	}
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

// auraRequest is the Workers AI Deepgram Aura TTS body.
type auraRequest struct {
	Text     string `json:"text"`
	Speaker  string `json:"speaker"`
	Encoding string `json:"encoding"`
}

// Synthesize calls Workers AI TTS (Deepgram Aura).
func (c *Client) Synthesize(ctx context.Context, req SpeechRequest) ([]byte, string, error) {
	if ctx == nil {
		return nil, "", derrors.New(derrors.CodeInvalid, opSynthesize, "context required")
	}
	if c == nil {
		return nil, "", derrors.New(derrors.CodeNotReady, opSynthesize, "not configured")
	}
	input := strings.TrimSpace(req.Input)
	if input == "" {
		return nil, "", derrors.New(derrors.CodeInvalid, opSynthesize, "empty input")
	}
	model := NormalizeModel(strings.TrimSpace(req.Model))
	if model == "" {
		model = c.speech.TTSModel
	}
	speaker := catalog.FirstNonEmpty(req.Voice, c.speech.Voice) // c.speech.Voice is never empty (withBuiltins).
	encoding := catalog.FirstNonEmpty(req.ResponseFormat, "mp3")

	payload, err := json.Marshal(auraRequest{Text: input, Speaker: speaker, Encoding: encoding})
	if err != nil {
		return nil, "", derrors.Wrap(err, derrors.CodeInternal, opSynthesize, "marshal")
	}

	target := runURL(c.apiBase, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, "", derrors.Wrap(err, derrors.CodeInternal, opSynthesize, "request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "audio/mpeg,application/octet-stream,application/json")

	resp, err := c.runOnce(ctx, opSynthesize, httpReq)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	audio, contentType, err := readAudioBody(resp)
	if err != nil {
		return nil, "", err
	}
	if len(audio) == 0 {
		return nil, "", derrors.New(derrors.CodeUnavailable, opSynthesize, "empty audio").
			With("target", target)
	}
	return audio, contentType, nil
}

// Transcribe calls Workers AI STT (Deepgram Nova-3).
func (c *Client) Transcribe(ctx context.Context, req TranscriptionRequest) (string, error) {
	if ctx == nil {
		return "", derrors.New(derrors.CodeInvalid, opTranscribe, "context required")
	}
	if c == nil {
		return "", derrors.New(derrors.CodeNotReady, opTranscribe, "not configured")
	}
	if len(req.Audio) == 0 {
		return "", derrors.New(derrors.CodeInvalid, opTranscribe, "empty audio")
	}
	model := NormalizeModel(strings.TrimSpace(req.Model))
	if model == "" {
		model = c.speech.STTModel
	}

	mime := mimeFromFilename(req.Filename)
	if mime == "" {
		mime = "audio/mpeg"
	}

	q := url.Values{}
	q.Set("language", catalog.FirstNonEmpty(req.Language, c.speech.Language))

	target := runURL(c.apiBase, model)
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(req.Audio))
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, opTranscribe, "request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", mime)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.runOnce(ctx, opTranscribe, httpReq)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadCapped(resp.Body, runMaxBody, opTranscribe)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", upstreamError(opTranscribe, resp, body)
	}
	return parseTranscript(body)
}

func readAudioBody(resp *http.Response) ([]byte, string, error) {
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	body, err := httpx.ReadCapped(resp.Body, runMaxBody, opSpeech)
	if err != nil {
		return nil, contentType, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, contentType, upstreamError(opSpeech, resp, body)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "application/json") && !looksLikeJSON(body) {
		return body, contentType, nil // raw audio bytes.
	}
	audio, err := audioFromEnvelope(resp, body)
	return audio, contentType, err
}

// audioFromEnvelope unwraps a Workers AI JSON reply: an error envelope becomes an
// error, and the audio is read from a base64 string result or a {"audio": "<base64>"}
// object result (the MeloTTS shape). Anything else (no result, another shape, a string
// that is not base64) is an error, never JSON bytes passed off as audio.
func audioFromEnvelope(resp *http.Response, body []byte) ([]byte, error) {
	var envelope struct {
		Success bool             `json:"success"`
		Errors  []workersAIError `json:"errors"`
		Result  json.RawMessage  `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, opSpeech, "parse json")
	}
	if !envelope.Success {
		if rl, ok := ratelimit.Classify(opSpeech, resp.StatusCode, resp.Header, body); ok {
			return nil, rl
		}
		return nil, derrors.New(derrors.CodeUnavailable, opSpeech, firstErrorMessage(envelope.Errors, "cloudflare workers ai error"))
	}
	result := bytes.TrimSpace(envelope.Result)
	var audioStr string
	switch {
	case len(result) == 0 || string(result) == "null":
		return nil, derrors.New(derrors.CodeUnavailable, opSpeech, "success envelope with no audio")
	case result[0] == '"':
		if err := json.Unmarshal(result, &audioStr); err != nil {
			return nil, derrors.Wrap(err, derrors.CodeUnavailable, opSpeech, "parse result string")
		}
	case result[0] == '{':
		var obj struct {
			Audio string `json:"audio"`
		}
		if err := json.Unmarshal(result, &obj); err != nil {
			return nil, derrors.Wrap(err, derrors.CodeUnavailable, opSpeech, "parse result object").
				With("body", httpx.Truncate(string(result), errorBodyMax))
		}
		audioStr = obj.Audio
	default:
		return nil, derrors.New(derrors.CodeUnavailable, opSpeech, "unrecognised speech result").
			With("body", httpx.Truncate(string(result), errorBodyMax))
	}
	if strings.TrimSpace(audioStr) == "" {
		return nil, derrors.New(derrors.CodeUnavailable, opSpeech, "unrecognised speech result").
			With("body", httpx.Truncate(string(result), errorBodyMax))
	}
	audio, err := base64.StdEncoding.DecodeString(audioStr)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, opSpeech, "speech result is not base64")
	}
	return audio, nil
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

	return "", derrors.New(derrors.CodeUnavailable, opTranscribe, "no transcript in response")
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
