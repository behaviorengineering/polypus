package cloudflare

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func jsonSpeechResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}}
}

func TestAudioFromEnvelopeReadsTheAudioShapes(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte("mp3-bytes"))
	for _, body := range []string{
		`{"success":true,"result":"` + enc + `"}`,
		`{"success":true,"result":{"audio":"` + enc + `"}}`,
	} {
		audio, err := audioFromEnvelope(jsonSpeechResponse(), []byte(body))
		if err != nil || string(audio) != "mp3-bytes" {
			t.Fatalf("%s: audio %q err %v", body, audio, err)
		}
	}
}

func TestAudioFromEnvelopeRejectsWhatIsNotAudio(t *testing.T) {
	for _, body := range []string{
		`{"success":true}`,
		`{"success":true,"result":null}`,
		`{"success":true,"result":{"text":"hello"}}`,
		`{"success":true,"result":{"audio":""}}`,
		`{"success":true,"result":"not base64!"}`,
		`{"success":true,"result":[1,2]}`,
	} {
		audio, err := audioFromEnvelope(jsonSpeechResponse(), []byte(body))
		if derrors.CodeOf(err) != derrors.CodeUnavailable {
			t.Fatalf("%s: audio %q err %v, want an unavailable error", body, audio, err)
		}
		if strings.Contains(string(audio), "success") {
			t.Fatalf("%s: the JSON envelope came back as audio", body)
		}
	}
}
