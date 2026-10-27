package deploy

import (
	"fmt"
	"os"
)

func setProcessEnv(key, value string) error {
	if err := os.Setenv(key, value); err != nil {
		return fmt.Errorf("deploy: setenv %s: %w", key, err)
	}
	return nil
}

func unsetProcessEnv(key string) error {
	if err := os.Unsetenv(key); err != nil {
		return fmt.Errorf("deploy: unsetenv %s: %w", key, err)
	}
	return nil
}
