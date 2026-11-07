package polypus_test

import (
	"io"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/behaviorengineering/polypus/pkg/polypus"
)

// exportedFields lists t's exported fields as "Name Type".
func exportedFields(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		if f.IsExported() {
			out = append(out, f.Name+" "+f.Type.String())
		}
	}
	return out
}

// TestFacadeFieldSetsArePinned keeps the aliased option and result types a
// reviewed contract: a field added to the internal struct for internal reasons
// fails here until the façade accepts it as public API.
func TestFacadeFieldSetsArePinned(t *testing.T) {
	for name, tc := range map[string]struct {
		typ  reflect.Type
		want []string
	}{
		"ServeOptions": {
			typ: reflect.TypeFor[polypus.ServeOptions](),
			want: []string{
				"Host string",
				"Port int",
				"BackendURL string",
				"Keyring operatorconfig.Keyring",
				"ConfigPath string",
				"BaseURL string",
			},
		},
		"SmokeOptions": {
			typ: reflect.TypeFor[polypus.SmokeOptions](),
			want: []string{
				"BaseURL string",
				"Channels []string",
				"ChatModel string",
				"TTSModel string",
				"STTModel string",
				"SystemOneModel string",
				"BatchModel string",
				"TTSVoice string",
				"TTSText string",
				"AudioOutPath string",
				"RequireCF bool",
				"RequireSelectedModel bool",
				"AllowedSelectedModels []string",
				"Temperature float64",
				"MaxTokens int",
				"APIKey string",
				"ReasoningEffort string",
			},
		},
		"SmokeResult": {
			typ: reflect.TypeFor[polypus.SmokeResult](),
			want: []string{
				"Channel string",
				"Model string",
				"SelectedModel string",
				"Probe string",
				"Status smoke.Status",
				"Ms int64",
				"Detail string",
			},
		},
	} {
		if got := exportedFields(tc.typ); !slices.Equal(got, tc.want) {
			t.Errorf("polypus.%s fields changed; review the public contract, then update this list.\ngot  %q\nwant %q", name, got, tc.want)
		}
	}
}

// The entry points' signatures are part of the contract: a change fails to compile
// here, so it is reviewed as a kit API change.
var (
	_ func(polypus.ServeOptions, ...polypus.HandlerOption) (polypus.Handler, error) = polypus.NewHandler
	_ http.Handler                                                                  = polypus.Handler(nil)
	_ io.Closer                                                                     = polypus.Handler(nil)
)
