package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestParseChatRequestReadsEveryRoutingField(t *testing.T) {
	req, err := parseChatRequest([]byte(`{"model":" b/m ","stream":true,"reasoning":{"effort":"high"},
		"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://x/y.png"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.model != "b/m" || !req.stream || !req.vision || !req.thinking {
		t.Fatalf("one decode must give model, stream, vision and thinking: %+v", req)
	}
}

func TestParseChatRequestRejectsBadBodies(t *testing.T) {
	for _, body := range []string{`not json`, `{"messages":[]}`, `{"model":"  "}`} {
		if _, err := parseChatRequest([]byte(body)); derrors.CodeOf(err) != derrors.CodeInvalid {
			t.Fatalf("%s: want invalid, got %v", body, err)
		}
	}
}

func TestParseChatRequestNamesAWrongFieldType(t *testing.T) {
	for body, want := range map[string]string{
		`{"model":"m","stream":"true"}`: "stream must be a boolean",
		`{"model":42}`:                  "model must be a string",
	} {
		_, err := parseChatRequest([]byte(body))
		if derrors.CodeOf(err) != derrors.CodeInvalid || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: want %q, got %v", body, want, err)
		}
	}
	req, err := parseChatRequest([]byte(`{"model":"m","stream":null}`))
	if err != nil || req.stream {
		t.Fatalf("a null stream is the default, non-streaming: %+v %v", req, err)
	}
}

func TestOutboundSetsTheDownstreamModelAndMapsThinking(t *testing.T) {
	req, err := parseChatRequest([]byte(`{"model":"cf/glm","reasoning":{"effort":"high"},"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := req.outbound("glm-4.7-flash", config.ExtensionCloudflare)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := applyChatThinking([]byte(`{"model":"glm-4.7-flash","reasoning":{"effort":"high"},"messages":[]}`),
		config.ExtensionCloudflare, "glm-4.7-flash")
	var got, wantMap map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantMap); err != nil {
		t.Fatal(err)
	}
	if got[keyModel] != "glm-4.7-flash" || len(got) != len(wantMap) {
		t.Fatalf("outbound = %s, want the same fields as %s", out, want)
	}
}

func TestChatRequestKeepsLargeIntegersExact(t *testing.T) {
	const seed = "9007199254740993" // 2^53 + 1: a float64 round trip changes it.
	req, err := parseChatRequest([]byte(`{"model":"m","seed":` + seed + `,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := req.outbound("down", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"seed":`+seed) {
		t.Fatalf("seed changed on re-encode: %s", out)
	}
}
