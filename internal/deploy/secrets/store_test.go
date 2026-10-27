package secrets

import (
	"testing"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

func TestStoreAndLookup(t *testing.T) {
	t.Setenv(ClientKeyName, "")
	kr := operatorconfig.NewMemKeyring()
	if got, err := Lookup(kr, "polypus-work", ClientKeyName); err != nil || got != "" {
		t.Fatalf("missing key: got %q, %v want empty, nil", got, err)
	}
	if err := Store(kr, "polypus-work", ClientKeyName, " pk_abc \n"); err != nil {
		t.Fatal(err)
	}
	if got, err := Lookup(kr, "polypus-work", ClientKeyName); err != nil || got != "pk_abc" {
		t.Fatalf("stored key: got %q, %v", got, err)
	}
	if got, _ := Lookup(kr, "polypus-other", ClientKeyName); got != "" {
		t.Fatalf("keyring services must stay apart: got %q", got)
	}
}

func TestLookupEnvWins(t *testing.T) {
	t.Setenv(ClientKeyName, "from-env")
	kr := operatorconfig.NewMemKeyring()
	if err := Store(kr, "polypus-work", ClientKeyName, "from-keyring"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Lookup(kr, "polypus-work", ClientKeyName); got != "from-env" {
		t.Fatalf("got %q want from-env", got)
	}
}

func TestStoreRejectsEmpty(t *testing.T) {
	if err := Store(operatorconfig.NewMemKeyring(), "polypus-work", "X", "  "); err == nil {
		t.Fatal("expected an empty value rejected")
	}
}
