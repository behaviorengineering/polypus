package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNonEmptyDotenvKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.deploy")
	content := `# comment
CF_AI_API_KEY=secret
CF_ACCOUNT_ID=
OPENROUTER_API_KEY="or-key"
export GEMINI_API_KEY_FREE=''
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := NonEmptyDotenvKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got["CF_AI_API_KEY"] || got["CF_ACCOUNT_ID"] || got["GEMINI_API_KEY_FREE"] {
		t.Fatalf("unexpected map: %v", got)
	}
	if !got["OPENROUTER_API_KEY"] {
		t.Fatal("expected quoted openrouter key")
	}
}
