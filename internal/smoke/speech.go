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
		return truncate(text, 80), nil
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
		return nil, err
	}
	req, err := httpNewPost(ctx, opts.BaseURL+"/v1/audio/speech", string(raw))
	if err != nil {
		return nil, err
	}
	resp, err := httpDefaultDo(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := ioReadAllLimit(resp.Body, 8<<20)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &statusError{what: "speech", status: resp.StatusCode, body: truncate(string(body), 200)}
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
		return "", err
	}
	part, err := w.CreateFormFile("file", "smoke.mp3")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.BaseURL+"/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := httpDefaultDo(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := ioReadAllLimit(resp.Body, 1<<20)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &statusError{what: "transcription", status: resp.StatusCode, body: truncate(string(raw), 200)}
	}
	var flat struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return "", err
	}
	return flat.Text, nil
}

// truncate trims s and caps it at n runes, ending a cut string with "...". It cuts on
// rune boundaries, so model output in any script stays valid text.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(0, n-3)]) + "..."
}
