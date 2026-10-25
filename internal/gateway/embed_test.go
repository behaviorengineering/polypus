package gateway

import "testing"

func TestEmbeddingsURL(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{base: "https://openrouter.ai/api/v1", want: "https://openrouter.ai/api/v1/embeddings"},
		{base: "http://127.0.0.1:1234", want: "http://127.0.0.1:1234/v1/embeddings"},
		{base: "https://api.deepinfra.com/v1/openai", want: "https://api.deepinfra.com/v1/openai/embeddings"},
		{base: "https://api.deepinfra.com/v1/openai/", want: "https://api.deepinfra.com/v1/openai/embeddings"},
	}
	for _, tc := range cases {
		got := embeddingsURL(tc.base)
		if got != tc.want {
			t.Fatalf("base %q: got %q want %q", tc.base, got, tc.want)
		}
	}
}
