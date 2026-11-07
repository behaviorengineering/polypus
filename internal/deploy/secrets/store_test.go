package secrets

import (
	"testing"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestStoreTrimsAndKeepsServicesApart(t *testing.T) {
	t.Setenv(ClientKeyName, "")
	kr := operatorconfig.NewMemKeyring()
	if err := Store(kr, "polypus-work", ClientKeyName, " pk_abc \n"); err != nil {
		t.Fatal(err)
	}
	r := &Resolver{Keyring: kr}
	if got, src, err := r.Resolve("polypus-work", ClientKeyName); err != nil || got != "pk_abc" || src != SourceKeyring {
		t.Fatalf("stored key: got %q, %q, %v", got, src, err)
	}
	if got, _, _ := r.Resolve("polypus-other", ClientKeyName); got != "" {
		t.Fatalf("keyring services must stay apart: got %q", got)
	}
}

func TestStoreRejectsEmpty(t *testing.T) {
	err := Store(operatorconfig.NewMemKeyring(), "polypus-work", "X", "  ")
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("err = %v, want an empty value refused as invalid", err)
	}
}

func TestStoreKeyringFailureIsAPrecondition(t *testing.T) {
	err := Store(brokenKeyring{}, "polypus-work", "X", "v")
	if derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("err = %v, want a keyring failure coded failed_precondition", err)
	}
}
