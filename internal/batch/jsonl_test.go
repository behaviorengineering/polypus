package batch

import (
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestParseBatchJSONLNamesTheLineOfEveryRefusal(t *testing.T) {
	const ok = `{"custom_id":"a","method":"POST","url":"/v1/chat/completions","body":{"model":"m","messages":[]}}`
	for _, tc := range []struct {
		name, second, line string
	}{
		{"bad json", `{`, "2"},
		{"no custom_id", `{"method":"POST","url":"/v1/chat/completions","body":{"model":"m","messages":[]}}`, "2"},
		{"mixed models", `{"custom_id":"b","method":"POST","url":"/v1/chat/completions","body":{"model":"other","messages":[]}}`, "2"},
		{"duplicate custom_id", ok, "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBatchJSONL([]byte(ok+"\n"+tc.second+"\n"), EndpointChatCompletions)
			if derrors.CodeOf(err) != derrors.CodeInvalid || derrors.Field(err, "line") != tc.line {
				t.Fatalf("want CodeInvalid on line %s, got %v (line %q)", tc.line, err, derrors.Field(err, "line"))
			}
		})
	}
}

func TestParseBatchJSONLKeepsLinesAndModel(t *testing.T) {
	raw := `{"custom_id":"a","method":"POST","url":"/v1/embeddings","body":{"model":"e","input":"x"}}
{"custom_id":"b","method":"POST","url":"/v1/embeddings","body":{"model":"e","input":"y"}}`
	in, err := ParseBatchJSONL([]byte(raw), EndpointEmbeddings)
	if err != nil {
		t.Fatal(err)
	}
	if in.Model != "e" || len(in.Lines) != 2 || in.Lines[1].CustomID != "b" {
		t.Fatalf("parsed %+v", in)
	}
}
