package router

import (
	"encoding/json"
	"testing"
)

func TestRawProviderJSONCoercesEveryShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"empty bytes", []byte{}, ""},
		{"bytes", []byte(`{"a":1}`), `{"a":1}`},
		{"raw message", json.RawMessage(`{"b":2}`), `{"b":2}`},
		{"string", `{"c":3}`, `{"c":3}`},
		{"empty string", "", ""},
		{"decoded value", map[string]int{"d": 4}, `{"d":4}`},
	} {
		got, err := rawProviderJSON(tc.in)
		if err != nil || string(got) != tc.want {
			t.Fatalf("%s: got %q %v, want %q", tc.name, got, err, tc.want)
		}
		if tc.want == "" && got != nil {
			t.Fatalf("%s: an absent body must be nil", tc.name)
		}
	}
	if _, err := rawProviderJSON(func() {}); err == nil {
		t.Fatal("an unencodable value must fail")
	}
}
