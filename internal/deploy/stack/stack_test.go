package stack

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSharedFilesMatchRepoRoot fails when the embedded copies drift from the files
// local serve uses at the repository root.
func TestSharedFilesMatchRepoRoot(t *testing.T) {
	for name, embedded := range map[string][]byte{"otelcol.config.yaml": otelcolConfig, "images.env": imagePins} {
		root, err := os.ReadFile(filepath.Join("..", "..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(root, embedded) {
			t.Fatalf("internal/deploy/stack/%s drifted from the repository root's; run go tool task sync-deploy-stack", name)
		}
	}
}

func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "polypus-work")
	compose, err := Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	if compose != filepath.Join(dir, ComposeFileName) {
		t.Fatalf("compose path %q", compose)
	}
	for name := range files {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
	// A second deploy rewrites the same files.
	if _, err := Write(dir); err != nil {
		t.Fatal(err)
	}
}
