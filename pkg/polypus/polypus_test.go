package polypus

import (
	"context"
	"errors"
	"testing"
)

func TestInvalidOptionsReturnErrorsInsteadOfPanicking(t *testing.T) {
	for name, opt := range map[string]HandlerOption{
		"nil logger":      WithLogger(nil),
		"empty state dir": WithAdminStateDir("  "),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewHandler(ServeOptions{}, opt); CodeOf(err) != CodeInvalid {
				t.Fatalf("NewHandler: want CodeInvalid, got %v", err)
			}
			err := Serve(context.Background(), ServeOptions{}, opt)
			var pe *Error
			if !errors.As(err, &pe) || pe.Code() != CodeInvalid {
				t.Fatalf("Serve: want *polypus.Error with CodeInvalid, got %v", err)
			}
		})
	}
}

func TestZeroOptionIsIgnored(t *testing.T) {
	opts, err := gatewayOptions("test", []HandlerOption{{}, WithObservability("", nil)})
	if err != nil || len(opts) != 1 {
		t.Fatalf("want one gateway option and no error, got %d, %v", len(opts), err)
	}
}
