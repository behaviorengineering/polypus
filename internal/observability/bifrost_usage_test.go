package observability

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestBifrostRequestType_prefersFieldThenPointers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  *schemas.BifrostRequest
		want schemas.RequestType
	}{
		{name: "nil", want: ""},
		{
			name: "explicit field wins",
			req: &schemas.BifrostRequest{
				RequestType: schemas.OCRRequest,
				ChatRequest: &schemas.BifrostChatRequest{},
			},
			want: schemas.OCRRequest,
		},
		{
			name: "chat pointer",
			req:  &schemas.BifrostRequest{ChatRequest: &schemas.BifrostChatRequest{}},
			want: schemas.ChatCompletionRequest,
		},
		{
			name: "speech pointer",
			req:  &schemas.BifrostRequest{SpeechRequest: &schemas.BifrostSpeechRequest{}},
			want: schemas.SpeechRequest,
		},
		{
			name: "transcription pointer",
			req:  &schemas.BifrostRequest{TranscriptionRequest: &schemas.BifrostTranscriptionRequest{}},
			want: schemas.TranscriptionRequest,
		},
		{
			name: "ocr pointer",
			req:  &schemas.BifrostRequest{OCRRequest: &schemas.BifrostOCRRequest{}},
			want: schemas.OCRRequest,
		},
		{
			name: "rerank pointer",
			req:  &schemas.BifrostRequest{RerankRequest: &schemas.BifrostRerankRequest{}},
			want: schemas.RerankRequest,
		},
		{
			name: "compaction pointer",
			req:  &schemas.BifrostRequest{CompactionRequest: &schemas.BifrostCompactionRequest{}},
			want: schemas.CompactionRequest,
		},
		{
			name: "image generation pointer",
			req:  &schemas.BifrostRequest{ImageGenerationRequest: &schemas.BifrostImageGenerationRequest{}},
			want: schemas.ImageGenerationRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := bifrostRequestType(tc.req); got != tc.want {
				t.Fatalf("bifrostRequestType = %q, want %q", got, tc.want)
			}
		})
	}
}
