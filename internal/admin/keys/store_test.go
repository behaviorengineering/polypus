package keys

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func testStore(t *testing.T) (*Store, string) {
	path := t.TempDir() + "/keys.json"
	s, err := Config{
		Path:  path,
		Clock: func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) },
		Rand:  rand.Reader,
	}.CreateStore()
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestGenerateVerifyRotateDelete(t *testing.T) {
	s, path := testStore(t)
	gen, err := s.Generate(context.Background(), "ops")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(path), gen.Key) {
		t.Fatal("plaintext must not be stored")
	}
	if _, err := s.Verify(gen.Key); err != nil {
		t.Fatal(err)
	}
	rot, err := s.Rotate(context.Background(), "ops")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(gen.Key); err == nil {
		t.Fatal("old key must fail")
	}
	if _, err := s.Verify(rot.Key); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(rot.Key); err == nil {
		t.Fatal("deleted key must fail")
	}
}

func TestCreateStoreRequiresClock(t *testing.T) {
	_, err := Config{Path: "/tmp/x"}.CreateStore()
	if err == nil {
		t.Fatal("expected error")
	}
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func TestGenerateStoresSHA256(t *testing.T) {
	s, _ := testStore(t)
	gen, err := s.Generate(context.Background(), "ops")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gen.Record.Hash, "sha256$") {
		t.Fatalf("new key hash %q, want sha256$", gen.Record.Hash)
	}
	if _, err := s.Verify(gen.Key); err != nil {
		t.Fatal(err)
	}
	wrong := gen.Record.Prefix + ".not-the-secret"
	if _, err := s.Verify(wrong); derrors.CodeOf(err) != derrors.CodeUnauthorized {
		t.Fatalf("a wrong secret must be unauthorized, got %v", err)
	}
}

// writeLegacyKey stores one key whose hash is argon2id, as older stores wrote, and
// returns its token.
func writeLegacyKey(t testing.TB, s *Store, path string) string {
	t.Helper()
	const id, secret = "0123456789abcdef0123456789abcdef", "legacy-secret"
	rec := Record{ID: id, Name: "old", Prefix: tokenPrefix + id, Hash: legacyArgon2idHash(t, []byte(secret)), CreatedAt: "2025-01-01T00:00:00Z"}
	if err := s.write(filePayload{Version: 1, Keys: []Record{rec}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(path), "argon2id$") {
		t.Fatal("fixture should hold an argon2id hash")
	}
	return tokenPrefix + id + "." + secret
}

func TestLegacyArgonKeyUpgradesOnFirstUse(t *testing.T) {
	s, path := testStore(t)
	token := writeLegacyKey(t, s, path)
	ctx := context.Background()

	if open, err := s.Authorize(ctx, token+"x"); err == nil || open {
		t.Fatalf("a wrong secret must not open or upgrade: open=%v err=%v", open, err)
	}
	if strings.Contains(readFile(path), "sha256$") {
		t.Fatal("a failed check must not rewrite the hash")
	}
	if open, err := s.Authorize(ctx, token); err != nil || open {
		t.Fatalf("legacy key must authorize: open=%v err=%v", open, err)
	}
	raw := readFile(path)
	if strings.Contains(raw, "argon2id$") || !strings.Contains(raw, "sha256$") {
		t.Fatalf("file should now hold sha256$ for the key:\n%s", raw)
	}
	recs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if ok, legacy := verifyHash(recs[0].Hash, []byte("legacy-secret")); !ok || legacy {
		t.Fatalf("second check should use SHA-256: ok=%v legacy=%v", ok, legacy)
	}
	if _, err := s.Authorize(ctx, token); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeReloadsOnlyWhenFileChanges(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	gen, err := s.Generate(ctx, "ops")
	if err != nil {
		t.Fatal(err)
	}
	before := s.parses.Load()
	for range 5 {
		if _, err := s.Authorize(ctx, gen.Key); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.parses.Load() - before; got != 1 {
		t.Fatalf("five checks of an unchanged file parsed it %d times, want 1", got)
	}
	// A write from another process (here a second Store on the same path) replaces
	// the file, so the next check sees it.
	other, err := Config{Path: s.path, Clock: s.clock}.CreateStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Delete(ctx, "ops"); err != nil {
		t.Fatal(err)
	}
	before = s.parses.Load()
	open, err := s.Authorize(ctx, gen.Key)
	if err != nil || !open {
		t.Fatalf("after the key was deleted elsewhere the store is open: open=%v err=%v", open, err)
	}
	if got := s.parses.Load() - before; got != 1 {
		t.Fatalf("a changed file should be parsed once, got %d", got)
	}
}

func TestAuthorizeRequiresContext(t *testing.T) {
	s, _ := testStore(t)
	var ctx context.Context // Nil on purpose.
	if _, err := s.Authorize(ctx, "ppk.a.b"); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("nil ctx: %v", err)
	}
}

// BenchmarkAuthorize measures one request's key check: a stat of the cached key
// file and a SHA-256 compare. argon2id-verify is the per-request cost it replaces
// (before, every request also read and parsed the file).
func BenchmarkAuthorize(b *testing.B) {
	ctx := context.Background()
	b.Run("sha256", func(b *testing.B) {
		path := b.TempDir() + "/keys.json"
		s, err := Config{Path: path, Clock: time.Now, Rand: rand.Reader}.CreateStore()
		if err != nil {
			b.Fatal(err)
		}
		gen, err := s.Generate(ctx, "ops")
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := s.Authorize(ctx, gen.Key); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("argon2id-verify", func(b *testing.B) {
		enc := legacyArgon2idHash(b, []byte("legacy-secret"))
		b.ReportAllocs()
		for b.Loop() {
			if ok, _ := verifyHash(enc, []byte("legacy-secret")); !ok {
				b.Fatal("legacy hash must verify")
			}
		}
	})
}

func TestMutatorsNameTheKeyOperationOnALockOrWriteFailure(t *testing.T) {
	// A key file under a path that is a file, not a directory, can be neither locked
	// nor written.
	parent := t.TempDir() + "/not-a-dir"
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Config{
		Path:  parent + "/keys.json",
		Clock: func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) },
		Rand:  rand.Reader,
	}.CreateStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, genErr := s.Generate(ctx, "ops")
	_, rotErr := s.Rotate(ctx, "ops")
	delErr := s.Delete(ctx, "ops")
	for op, err := range map[string]error{"keys.Generate": genErr, "keys.Rotate": rotErr, "keys.Delete": delErr} {
		var de *derrors.Error
		if !errors.As(err, &de) || de.Op() != op {
			t.Fatalf("%s: want the key operation as the outer op, got %v", op, err)
		}
	}
}

func TestWriteNamesTheKeyStore(t *testing.T) {
	s, _ := testStore(t)
	// The key file's parent is a file, so the write cannot land.
	blocker := t.TempDir() + "/f"
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.path = blocker + "/keys.json"
	err := s.write(filePayload{})
	var de *derrors.Error
	if !errors.As(err, &de) || de.Op() != "keys.write" || derrors.Field(err, "path") != s.path {
		t.Fatalf("a failed write must name keys.write and the path, got %v", err)
	}
}
