package statefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteAtomicSyncsFileAndDir checks the temp file is synced before the rename and
// the directory after it, so a crash keeps either the old file or the new one. The
// batch store writes through WriteAtomic, so this covers its files too.
func TestWriteAtomicSyncsFileAndDir(t *testing.T) {
	var synced []string
	prev := syncFile
	syncFile = func(f *os.File) error {
		synced = append(synced, f.Name())
		return prev(f)
	}
	t.Cleanup(func() { syncFile = prev })

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteAtomic(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(synced) != 2 {
		t.Fatalf("want the temp file and the directory synced, got %q", synced)
	}
	if filepath.Dir(synced[0]) != dir || !strings.HasPrefix(filepath.Base(synced[0]), ".tmp-") {
		t.Fatalf("first sync must be the temp file before the rename, got %q", synced[0])
	}
	if synced[1] != dir {
		t.Fatalf("second sync must be the directory after the rename, got %q", synced[1])
	}
	got, err := os.ReadFile(path) //nolint:gosec // G304: test temp dir
	if err != nil || string(got) != `{}` {
		t.Fatalf("content = %q, %v", got, err)
	}
}
