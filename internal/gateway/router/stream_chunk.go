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
	// The raw provider chunk only feeds observability, so one that cannot be encoded
	// is left out rather than failing the stream.
	raw, rawErr := rawProviderJSON(resp.ExtraFields.RawResponse)
	if rawErr != nil {
		raw = nil
	}
	return ChatStreamChunk{OpenAIJSON: openai, RawProviderJSON: raw}, nil
}
