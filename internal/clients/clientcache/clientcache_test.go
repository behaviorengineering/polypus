package clientcache

import (
	"errors"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestRegistryCreatesOnce(t *testing.T) {
	var r Registry[int]
	created := 0
	create := func() (*int, error) {
		created++
		v := created
		return &v, nil
	}
	a, _ := r.Get("k", "token-a", create)
	b, _ := r.Get("k", "token-a", create)
	if a != b || created != 1 {
		t.Fatalf("created=%d same=%v", created, a == b)
	}
	var fresh Registry[int]
	if c, _ := fresh.Get("k", "token-a", create); c == a {
		t.Fatal("a new registry must build its own client")
	}
}

func TestRegistryReplacesClientOnTokenChange(t *testing.T) {
	var r Registry[string]
	build := func(token string) func() (*string, error) {
		return func() (*string, error) { return &token, nil }
	}
	old, err := r.Get("cf", "old-token\x00https://base", build("old-token"))
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := r.Get("cf", "new-token\x00https://base", build("new-token"))
	if err != nil {
		t.Fatal(err)
	}
	if rotated == old || *rotated != "new-token" {
		t.Fatalf("a rotated token must get a new client, got %q", *rotated)
	}
	if again, _ := r.Get("cf", "new-token\x00https://base", build("unused")); again != rotated {
		t.Fatal("the same fingerprint must reuse the client")
	}
	if moved, _ := r.Get("cf", "new-token\x00https://other", build("moved")); moved == rotated {
		t.Fatal("a changed base must get a new client")
	}
	if n := len(r.clients); n != 1 {
		t.Fatalf("one entry per backend after rotation, got %d", n)
	}
	for _, e := range r.clients {
		if e.client == old || *e.client == "old-token" {
			t.Fatal("the registry must not hold the client for the old token")
		}
	}
}

func TestForBackendResolvesTokenAndRebuildsOnRotation(t *testing.T) {
	var r Registry[string]
	b := config.BackendDef{ID: "be", Auth: config.BackendAuth{BearerEnv: "CLIENTCACHE_TEST_TOKEN"}}
	build := func(token string) (*string, error) { return &token, nil }

	t.Setenv("CLIENTCACHE_TEST_TOKEN", "tok-a")
	a, err := r.ForBackend("test.Get", b, "https://base", build)
	if err != nil || *a != "tok-a" {
		t.Fatalf("first build = %v, %v", a, err)
	}
	if again, _ := r.ForBackend("test.Get", b, "https://base", build); again != a {
		t.Fatal("the same token and base must reuse the client")
	}
	t.Setenv("CLIENTCACHE_TEST_TOKEN", "tok-b")
	if rotated, _ := r.ForBackend("test.Get", b, "https://base", build); rotated == a || *rotated != "tok-b" {
		t.Fatal("a rotated token must build a new client")
	}
}

func TestForBackendKeepsTheCodes(t *testing.T) {
	var r Registry[string]
	b := config.BackendDef{ID: "be", Auth: config.BackendAuth{BearerEnv: "CLIENTCACHE_TEST_UNSET"}}
	t.Setenv("CLIENTCACHE_TEST_UNSET", "")
	_, err := r.ForBackend("test.Get", b, "https://base", func(string) (*string, error) { return nil, nil })
	if derrors.CodeOf(err) != derrors.CodeNotReady || derrors.Field(err, "backend") != "be" {
		t.Fatalf("missing token: err = %+v, want not_ready with the backend", err)
	}

	t.Setenv("CLIENTCACHE_TEST_UNSET", "tok")
	boom := errors.New("boom")
	_, err = r.ForBackend("test.Get", b, "https://base", func(string) (*string, error) { return nil, boom })
	if !errors.Is(err, boom) || derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("build failure: err = %+v, want the cause as invalid", err)
	}

	var nilReg *Registry[string]
	if _, err := nilReg.ForBackend("test.Get", b, "", func(string) (*string, error) { return nil, nil }); derrors.CodeOf(err) != derrors.CodeInternal {
		t.Fatalf("nil registry: err = %v", err)
	}
}
