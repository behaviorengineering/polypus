package cli

import "fmt"

type exitError struct {
	code int
	msg  string
	err  error
}

func (e *exitError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("exit status %d", e.code)
}

func (e *exitError) Unwrap() error {
	return e.err
}

func exitStatus(code int) error {
	if code == 0 {
		return nil
	}
	return &exitError{code: code}
}
