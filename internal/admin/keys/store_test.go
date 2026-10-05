package keys

import (
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"
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
	gen, err := s.Generate("ops")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(path), gen.Key) {
		t.Fatal("plaintext must not be stored")
	}
	if _, err := s.Verify(gen.Key); err != nil {
		t.Fatal(err)
	}
	rot, err := s.Rotate("ops")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(gen.Key); err == nil {
		t.Fatal("old key must fail")
	}
	if _, err := s.Verify(rot.Key); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("ops"); err != nil {
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
