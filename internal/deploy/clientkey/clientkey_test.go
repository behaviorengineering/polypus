package clientkey

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
)

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

// fakeDocker answers docker ps with container, and docker exec with execOut.
func fakeDocker(container, execOut string, execErr error, calls *[][]string) func(context.Context, []string) ([]byte, error) {
	return func(_ context.Context, args []string) ([]byte, error) {
		*calls = append(*calls, args)
		switch args[0] {
		case "ps":
			return []byte(container), nil
		case "exec":
			return []byte(execOut), execErr
		}
		return nil, errors.New("unexpected docker call")
	}
}

func TestCreateStoresKey(t *testing.T) {
	t.Setenv(secrets.ClientKeyName, "")
	kr := operatorconfig.NewMemKeyring()
	var calls [][]string
	name, err := Create(ctx(t), Config{
		Project: "polypus-work",
		Keyring: kr,
		Docker:  fakeDocker("polypus-work-gateway-1\n", `{"name":"client","id":"k1","key":"pk_secret"}`, nil, &calls),
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != "client" {
		t.Fatalf("name %q", name)
	}
	if got, _ := kr.Get("polypus-work", secrets.ClientKeyName); got != "pk_secret" {
		t.Fatalf("stored %q", got)
	}
	if !slices.Contains(calls[0], "label=com.docker.compose.project=polypus-work") {
		t.Fatalf("ps should filter by project: %v", calls[0])
	}
	want := []string{"exec", "polypus-work-gateway-1", "polypus", "admin-key", "generate", "--name", "client", "--yes", "--json"}
	if !slices.Equal(calls[1], want) {
		t.Fatalf("exec %v\nwant %v", calls[1], want)
	}
}

func TestCreateRotate(t *testing.T) {
	var calls [][]string
	_, err := Create(ctx(t), Config{
		Project: "polypus-work", KeyName: "laptop", Rotate: true,
		Keyring: operatorconfig.NewMemKeyring(),
		Docker:  fakeDocker("c\n", `{"key":"pk_new"}`, nil, &calls),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(calls[1], "rotate") || !slices.Contains(calls[1], "laptop") {
		t.Fatalf("exec %v, want rotate of laptop", calls[1])
	}
}

func TestCreateNoGateway(t *testing.T) {
	var calls [][]string
	_, err := Create(ctx(t), Config{Project: "polypus-work", Keyring: operatorconfig.NewMemKeyring(), Docker: fakeDocker("", "", nil, &calls)})
	if err == nil || !strings.Contains(err.Error(), "polypus deploy run first") {
		t.Fatalf("err = %v, want a pointer to deploy first", err)
	}
}

func TestCreateExecFails(t *testing.T) {
	kr := operatorconfig.NewMemKeyring()
	var calls [][]string
	_, err := Create(ctx(t), Config{Project: "polypus-work", Keyring: kr, Docker: fakeDocker("c\n", "", errors.New("name already exists"), &calls)})
	if err == nil || !strings.Contains(err.Error(), "name already exists") {
		t.Fatalf("err = %v", err)
	}
	if _, err := kr.Get("polypus-work", secrets.ClientKeyName); !errors.Is(err, operatorconfig.ErrNotFound) {
		t.Fatal("nothing should be stored when generate fails")
	}
}

func gateway(t *testing.T, key string, lockedStatus int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+key {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(lockedStatus)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerify(t *testing.T) {
	srv := gateway(t, "pk_secret", http.StatusUnauthorized)
	if err := Verify(ctx(t), srv.Client(), srv.URL, "pk_secret"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyGatewayStillOpen(t *testing.T) {
	srv := gateway(t, "pk_secret", http.StatusOK)
	if err := Verify(ctx(t), srv.Client(), srv.URL, "pk_secret"); err == nil || !strings.Contains(err.Error(), "want 401") {
		t.Fatalf("err = %v, want an open gateway reported", err)
	}
}

func TestVerifyWrongKey(t *testing.T) {
	srv := gateway(t, "pk_secret", http.StatusUnauthorized)
	if err := Verify(ctx(t), srv.Client(), srv.URL, "pk_other"); err == nil || !strings.Contains(err.Error(), "want 200") {
		t.Fatalf("err = %v, want the key rejected", err)
	}
}
