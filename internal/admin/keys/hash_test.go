package keys

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

// legacyArgon2idHash builds a hash the way the store wrote them before SHA-256,
// with the same cost settings.
func legacyArgon2idHash(t testing.TB, secret []byte) string {
	t.Helper()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	dk := argon2.IDKey(secret, salt, 2, 19456, 1, 32)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", 19456, 2, 1,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(dk))
}

func TestVerifyHashRoundTrip(t *testing.T) {
	enc := hashSecret([]byte("s3cret"))
	if !strings.HasPrefix(enc, "sha256$") {
		t.Fatalf("new hashes must be sha256$, got %q", enc)
	}
	if ok, legacy := verifyHash(enc, []byte("s3cret")); !ok || legacy {
		t.Fatalf("sha256 hash must accept its secret without the legacy flag: ok=%v legacy=%v", ok, legacy)
	}
	if ok, _ := verifyHash(enc, []byte("other")); ok {
		t.Fatal("sha256 hash must reject another secret")
	}
}

func TestVerifyHashLegacyArgon2id(t *testing.T) {
	enc := legacyArgon2idHash(t, []byte("s3cret"))
	if ok, legacy := verifyHash(enc, []byte("s3cret")); !ok || !legacy {
		t.Fatalf("argon2id hash must verify and be flagged legacy: ok=%v legacy=%v", ok, legacy)
	}
	if ok, _ := verifyHash(enc, []byte("other")); ok {
		t.Fatal("argon2id hash must reject another secret")
	}
}

func TestVerifyHashRejectsMalformed(t *testing.T) {
	for _, enc := range []string{
		"",
		"sha256$",
		"sha256$not-base64!",
		"sha256$" + base64.RawStdEncoding.EncodeToString([]byte("short")),
		"md5$abc",
	} {
		if ok, _ := verifyHash(enc, []byte("s3cret")); ok {
			t.Errorf("%q must fail verification", enc)
		}
	}
}

func TestVerifyHashRejectsBadCostSettings(t *testing.T) {
	enc := legacyArgon2idHash(t, []byte("s3cret"))
	good := "m=19456,t=2,p=1"
	for _, params := range []string{
		"m=19456,t=2",          // p missing: argon2 would panic on threads 0.
		"m=19456,t=0,p=1",      // time 0 panics.
		"m=19456,t=2,p=0",      // threads 0 panics.
		"m=4294967297,t=2,p=1", // overflows uint32.
		"m=2000000,t=2,p=1",    // over the memory cap.
		"m=19456,t=2,p=256",    // overflows uint8.
		"m=19456,t=2,p=1,x=1",  // unknown key.
		"m=19456,m=1,t=2,p=1",  // repeated key.
	} {
		if ok, _ := verifyHash(strings.Replace(enc, good, params, 1), []byte("s3cret")); ok {
			t.Errorf("params %q must fail verification", params)
		}
	}
}
