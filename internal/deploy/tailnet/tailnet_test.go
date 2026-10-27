package tailnet

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func fixed(out string, err error) StatusFunc {
	return func(context.Context) ([]byte, error) { return []byte(out), err }
}

const running = `{"BackendState":"Running","CurrentTailnet":{"Name":"me","MagicDNSSuffix":"corp.ts.net","MagicDNSEnabled":true}}`

func TestCheckRunningOnTailnet(t *testing.T) {
	if err := Check(context.Background(), "corp.ts.net", fixed(running, nil)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		out  string
		err  error
		want string
	}{
		"no cli":        {"", ErrNoCLI, "tailscale CLI not found"},
		"stopped":       {`{"BackendState":"Stopped"}`, nil, "not Running"},
		"no tailnet":    {`{"BackendState":"Running"}`, nil, "no current tailnet"},
		"other tailnet": {strings.Replace(running, "corp.ts.net", "home.ts.net", 1), nil, "on tailnet home.ts.net"},
		"magicdns off":  {strings.Replace(running, "true", "false", 1), nil, "MagicDNS is off"},
		"bad json":      {"{", nil, "parse"},
	} {
		t.Run(name, func(t *testing.T) {
			err := Check(context.Background(), "corp.ts.net", fixed(tc.out, tc.err))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want it to wrap %v", err, tc.err)
			}
		})
	}
}
