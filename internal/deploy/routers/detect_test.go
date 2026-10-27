package routers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasTopLevelRouters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `chat_backend:
  enabled: true
# routers:
#   x:
routers:
  investigator:
    capability: chat
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := HasTopLevelRouters(path)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestHasTopLevelRoutersCommentedOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `# routers:
#   x:
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := HasTopLevelRouters(path)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
