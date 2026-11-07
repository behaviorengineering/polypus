package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/httpx"
)

func runTTS(ctx context.Context, opts Options) ([]Result, error) {
	row := timed(ChannelTTS, "synthesize", opts.TTSModel, func() (string, error) {
		audio, err := synthesize(ctx, opts)
		if err != nil {
			return "", err
		}
		if len(audio) < 64 {
			return "", fmt.Errorf("audio too small (%d bytes)", len(audio))
		}
		return fmt.Sprintf("%d bytes", len(audio)), nil
	})
	if row.Status == StatusFail {
		return []Result{row}, fmt.Errorf("tts smoke failed")
	}
	return []Result{row}, nil
}

func runSTT(ctx context.Context, opts Options) ([]Result, error) {
	var audio []byte
	prep := timed(ChannelSTT, "tts_for_stt", opts.TTSModel, func() (string, error) {
		var err error
		audio, err = synthesize(ctx, opts)
		if err != nil {
			return "", err
		}
		if len(audio) < 64 {
			return "", fmt.Errorf("audio too small (%d bytes)", len(audio))
		}
		return fmt.Sprintf("%d bytes", len(audio)), nil
	})
	if prep.Status == StatusFail {
		return []Result{prep}, fmt.Errorf("stt smoke failed (tts prep)")
	}
	transcribe := timed(ChannelSTT, "transcribe", opts.STTModel, func() (string, error) {
		text, err := transcribeAudio(ctx, opts, audio)
		if err != nil {
			return "", err
		}
		if len(strings.TrimSpace(text)) < 8 {
			return "", fmt.Errorf("transcript too short: %q", text)
		}
		return httpx.Truncate(text, 80), nil
	})
	out := []Result{prep, transcribe}
	if transcribe.Status == StatusFail {
		return out, fmt.Errorf("stt smoke failed")
	}
	return out, nil
}

func synthesize(ctx context.Context, opts Options) ([]byte, error) {
	raw, err := json.Marshal(struct {
		Model          string `json:"model"`
		Input          string `json:"input"`
		Voice          string `json:"voice"`
		ResponseFormat string `json:"response_format"`
	}{opts.TTSModel, opts.TTSText, opts.TTSVoice, "mp3"})
	if err != nil {
		return nil, fmt.Errorf("speech: encode: %w", err)
	}
	req, err := httpNewPost(ctx, opts.BaseURL+"/v1/audio/speech", string(raw))
	if err != nil {
		return nil, fmt.Errorf("speech: request: %w", err)
	}
	body, _, err := call(req, "speech", audioBodyMax)
	if err != nil {
		return nil, err
	}
	if path := strings.TrimSpace(opts.AudioOutPath); path != "" {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return nil, fmt.Errorf("write audio out %q: %w", path, err)
		}
	}
	return body, nil
}

func transcribeAudio(ctx context.Context, opts Options, audio []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("model", opts.STTModel); err != nil {
		return "", fmt.Errorf("transcription: multipart: %w", err)
	}
	part, err := w.CreateFormFile("file", "smoke.mp3")
	if err != nil {
		return "", fmt.Errorf("transcription: multipart: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return "", fmt.Errorf("transcription: multipart: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("transcription: multipart: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.BaseURL+"/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", fmt.Errorf("transcription: request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	raw, _, err := call(req, "transcription", probeBodyMax)
	if err != nil {
		return "", err
	}
	var flat struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return "", fmt.Errorf("transcription: decode: %w", err)
	}
	return flat.Text, nil
}
