package keys

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
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

func verifyHash(encoded string, secret []byte) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}
	var memory uint32
	var time uint32
	var threads uint8
	for _, p := range strings.Split(parts[2], ",") {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return false
		}
		switch kv[0] {
		case "m":
			var m uint64
			if _, err := fmt.Sscanf(kv[1], "%d", &m); err != nil {
				return false
			}
			memory = uint32(m)
		case "t":
			var t uint64
			if _, err := fmt.Sscanf(kv[1], "%d", &t); err != nil {
				return false
			}
			time = uint32(t)
		case "p":
			var p uint64
			if _, err := fmt.Sscanf(kv[1], "%d", &p); err != nil {
				return false
			}
			threads = uint8(p)
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	got := argon2.IDKey(secret, salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
