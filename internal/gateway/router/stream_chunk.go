package router

import (
	"encoding/json"

	"github.com/maximhq/bifrost/core/schemas"
)

// ChatStreamChunk is one Bifrost OpenAI-shaped stream object plus optional provider raw JSON.
type ChatStreamChunk struct {
	OpenAIJSON      []byte
	RawProviderJSON []byte
}

func chatStreamChunkFromResponse(resp *schemas.BifrostChatResponse) (ChatStreamChunk, error) {
	if resp == nil {
		return ChatStreamChunk{}, nil
	}
	openai, err := json.Marshal(resp)
	if err != nil {
		return ChatStreamChunk{}, err
	}
	raw, _ := marshalRawProviderJSON(resp.ExtraFields.RawResponse)
	return ChatStreamChunk{OpenAIJSON: openai, RawProviderJSON: raw}, nil
}

func marshalRawProviderJSON(raw interface{}) ([]byte, bool) {
	if raw == nil {
		return nil, false
	}
	switch v := raw.(type) {
	case []byte:
		if len(v) == 0 {
			return nil, false
		}
		return v, true
	case string:
		if v == "" {
			return nil, false
		}
		return []byte(v), true
	case json.RawMessage:
		if len(v) == 0 {
			return nil, false
		}
		return []byte(v), true
	default:
		b, err := json.Marshal(v)
		if err != nil || len(b) == 0 {
			return nil, false
		}
		return b, true
	}
}
