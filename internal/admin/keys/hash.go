package keys

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 2
	argonMemory  = 19456
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

func hashSecret(secret []byte) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := argon2.IDKey(secret, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk),
	), nil
}

// Bounds for the cost settings read back from a stored hash. argon2.IDKey panics
// on zero time or threads, and the memory setting is an allocation per check, so
// a corrupt or hostile row must fail verification instead.
const (
	maxArgonTime   = 10
	maxArgonMemory = 1 << 20 // KiB (1 GiB).
	maxArgonKeyLen = 64
)

func verifyHash(encoded string, secret []byte) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}
	memory, time, threads, ok := parseArgonParams(parts[2])
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
	got := argon2.IDKey(secret, salt, time, memory, threads, uint32(len(want))) //nolint:gosec // G115: len(want) is at most maxArgonKeyLen
	return subtle.ConstantTimeCompare(got, want) == 1
}

// parseArgonParams reads "m=..,t=..,p=..". All three are required and each must
// be within the bounds above; ok is false otherwise.
func parseArgonParams(s string) (memory, time uint32, threads uint8, ok bool) {
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
			time = uint32(n)
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
	if len(seen) != 3 || time < 1 || time > maxArgonTime || threads < 1 ||
		memory < 8*uint32(threads) || memory > maxArgonMemory {
		return 0, 0, 0, false
	}
	return memory, time, threads, true
}
