package gateway

import (
	"encoding/json"
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
