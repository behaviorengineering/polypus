package keys

import (
	"strings"
	"testing"
)

func TestVerifyHashRoundTrip(t *testing.T) {
	enc, err := hashSecret([]byte("s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	if !verifyHash(enc, []byte("s3cret")) || verifyHash(enc, []byte("other")) {
		t.Fatal("round trip must accept the secret and reject another")
	}
}

func TestVerifyHashRejectsBadCostSettings(t *testing.T) {
	enc, err := hashSecret([]byte("s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	good := "m=19456,t=2,p=1"
	for _, params := range []string{
		"m=19456,t=2",          // p missing: argon2 would panic on threads 0
		"m=19456,t=0,p=1",      // time 0 panics
		"m=19456,t=2,p=0",      // threads 0 panics
		"m=4294967297,t=2,p=1", // overflows uint32
		"m=2000000,t=2,p=1",    // over the memory cap
		"m=19456,t=2,p=256",    // overflows uint8
		"m=19456,t=2,p=1,x=1",  // unknown key
		"m=19456,m=1,t=2,p=1",  // repeated key
	} {
		if verifyHash(strings.Replace(enc, good, params, 1), []byte("s3cret")) {
			t.Errorf("params %q must fail verification", params)
		}
	}
}
