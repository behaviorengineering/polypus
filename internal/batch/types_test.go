package batch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBatchMetaPublicOmitsAdapterFields(t *testing.T) {
	t.Parallel()
	meta := BatchMeta{
		ID:          "batch_test",
		Endpoint:    "/v1/chat/completions",
		Status:      BatchStatusInProgress,
		BackendID:   "cf_local",
		PublicModel: "cf_local/@cf/meta/llama",
		CFModel:     "@cf/meta/llama",
		CFRequestID: "cf-req-1",
	}
	raw, err := json.Marshal(meta.Public())
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, leak := range []string{"backend_id", "public_model", "cf_model", "cf_request_id"} {
		if strings.Contains(body, leak) {
			t.Fatalf("public JSON leaks %q: %s", leak, body)
		}
	}
}
