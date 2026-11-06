// Package keys stores the gateway's named access keys (ppk.<id>.<secret>) as
// SHA-256 digests in a locked JSON file and verifies presented tokens.
//
// Each secret is 32 random bytes, so a fast hash with a constant-time compare is
// as strong as a password KDF and costs microseconds per request. Hashes written
// before that change are argon2id; they still verify, and the store rewrites each
// as SHA-256 the first time its key is used.
package keys

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// sha256Prefix starts a stored SHA-256 digest; legacyPrefix a stored argon2id hash.
const (
	sha256Prefix = "sha256$"
	legacyPrefix = "argon2id$"
)

// hashSecret returns the stored form of secret: "sha256$" and the base64 digest.
func hashSecret(secret []byte) string {
	sum := sha256.Sum256(secret)
	return sha256Prefix + base64.RawStdEncoding.EncodeToString(sum[:])
}

// verifyHash reports whether secret matches encoded. legacy is true when encoded
// is an argon2id hash, which the store upgrades after a match.
func verifyHash(encoded string, secret []byte) (ok, legacy bool) {
	switch {
	case strings.HasPrefix(encoded, sha256Prefix):
		want, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, sha256Prefix))
		if err != nil || len(want) != sha256.Size {
			return false, false
		}
		got := sha256.Sum256(secret)
		return subtle.ConstantTimeCompare(got[:], want) == 1, false
	case strings.HasPrefix(encoded, legacyPrefix):
		return verifyArgon2id(encoded, secret), true
	default:
		return false, false
	}
}

// Bounds for the cost settings read back from a stored hash. argon2.IDKey panics
// on zero time or threads, and the memory setting is an allocation per check, so
// a corrupt or hostile row must fail verification instead.
const (
	maxArgonTime   = 10
	maxArgonMemory = 1 << 20 // KiB (1 GiB).
	maxArgonKeyLen = 64
)

// verifyArgon2id checks secret against a legacy argon2id hash.
func verifyArgon2id(encoded string, secret []byte) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}
	memory, passes, threads, ok := parseArgonParams(parts[2])
	if !ok {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(want) == 0 || len(want) > maxArgonKeyLen {
		return false
	}
	got := argon2.IDKey(secret, salt, passes, memory, threads, uint32(len(want))) //nolint:gosec // G115: len(want) is at most maxArgonKeyLen
	return subtle.ConstantTimeCompare(got, want) == 1
}

// parseArgonParams reads "m=..,t=..,p=..". All three are required and each must
// be within the bounds above; ok is false otherwise.
func parseArgonParams(s string) (memory, passes uint32, threads uint8, ok bool) {
	seen := map[string]bool{}
	for _, kv := range strings.Split(s, ",") {
		key, val, found := strings.Cut(kv, "=")
		if !found || seen[key] {
			return 0, 0, 0, false
		}
		seen[key] = true
		switch key {
		case "m":
			n, err := strconv.ParseUint(val, 10, 32)
			if err != nil {
				return 0, 0, 0, false
			}
			memory = uint32(n)
		case "t":
			n, err := strconv.ParseUint(val, 10, 32)
			if err != nil {
				return 0, 0, 0, false
			}
			passes = uint32(n)
		case "p":
			n, err := strconv.ParseUint(val, 10, 8)
			if err != nil {
				return 0, 0, 0, false
			}
			threads = uint8(n)
		default:
			return 0, 0, 0, false
		}
	}
	if len(seen) != 3 || passes < 1 || passes > maxArgonTime || threads < 1 ||
		memory < 8*uint32(threads) || memory > maxArgonMemory {
		return 0, 0, 0, false
	}
	return memory, passes, threads, true
}
