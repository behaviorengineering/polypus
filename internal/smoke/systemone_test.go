package smoke

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestSystemOneBillingSkipReadsTheStatus(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&statusError{what: "systemone", status: http.StatusPaymentRequired}, true},
		{fmt.Errorf("wrapped: %w", &statusError{what: "systemone", status: 400, body: "Use BYOK for this model"}), true},
		{&statusError{what: "systemone", status: http.StatusBadGateway, body: "upstream down"}, false},
		{errors.New("systemone status 402: said a string"), false},
		{nil, false},
	} {
		if got := isSystemOneBillingSkip(tc.err); got != tc.want {
			t.Fatalf("%v: got %v, want %v", tc.err, got, tc.want)
		}
	}
}
