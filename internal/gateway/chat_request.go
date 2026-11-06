package gateway

import (
	"bytes"
	"encoding/json"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// chatRequest is a chat completion body decoded once per request. Routing reads its
// fields; outbound encodes the body the backend gets.
type chatRequest struct {
	// raw is the body as the client sent it.
	raw      []byte
	root     map[string]any
	model    string
	stream   bool
	vision   bool
	thinking bool
}

func parseChatRequest(body []byte) (chatRequest, error) {
	const op = "gateway.parseChatRequest"
	// UseNumber keeps 64-bit integers such as seed exact when the body is re-encoded.
	var root map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return chatRequest{}, derrors.Wrap(err, derrors.CodeInvalid, op, "invalid json")
	}
	if dec.More() {
		return chatRequest{}, derrors.New(derrors.CodeInvalid, op, "invalid json: trailing data")
	}
	model, _ := root[keyModel].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return chatRequest{}, derrors.New(derrors.CodeInvalid, op, "model required")
	}
	stream, _ := root["stream"].(bool)
	return chatRequest{
		raw:      body,
		root:     root,
		model:    model,
		stream:   stream,
		vision:   chatMapHasVision(root),
		thinking: chatMapWantsThinking(root),
	}, nil
}

// outbound encodes the body for the backend: the model set to downstream and the
// reasoning fields mapped for the backend's extension. It edits the decoded request,
// so a request is sent once.
func (c chatRequest) outbound(downstream, extension string) ([]byte, error) {
	c.root[keyModel] = downstream
	applyChatThinkingRoot(c.root, extension, downstream)
	out, err := json.Marshal(c.root)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.chatRequest.outbound", "marshal")
	}
	return out, nil
}

// chatMapHasVision reports whether any message has an image_url content part.
func chatMapHasVision(root map[string]any) bool {
	messages, _ := root["messages"].([]any)
	for _, msg := range messages {
		m, _ := msg.(map[string]any)
		parts, ok := m["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if typ, ok := p["type"].(string); ok && strings.EqualFold(strings.TrimSpace(typ), "image_url") {
				return true
			}
		}
	}
	return false
}
