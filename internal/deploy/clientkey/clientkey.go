// Package clientkey creates the gateway client key for a deploy environment: it runs
// polypus admin-key inside the environment's running gateway container, stores the
// key in the environment's keyring service, and checks the gateway now needs it.
package clientkey

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// DefaultKeyName names the key in the gateway's key store.
const DefaultKeyName = "client"

// Config describes one client-key run.
type Config struct {
	// Project is the environment's compose project, polypus-<identifier>. It is also
	// the keyring service the key is stored under.
	Project string
	// KeyName names the key in the gateway's store; empty uses DefaultKeyName.
	KeyName string
	// Rotate replaces an existing key of that name instead of adding one.
	Rotate bool
	// Keyring stores the key; nil uses the OS keyring.
	Keyring operatorconfig.Keyring
	// Docker runs a docker command and returns its stdout; nil runs docker.
	Docker func(ctx context.Context, args []string) ([]byte, error)
}

func (c Config) keyName() string {
	if n := strings.TrimSpace(c.KeyName); n != "" {
		return n
	}
	return DefaultKeyName
}

func (c Config) docker(ctx context.Context, args []string) ([]byte, error) {
	if c.Docker != nil {
		return c.Docker(ctx, args)
	}
	return execx.StdoutContext(ctx, "docker", args)
}

// Create generates (or rotates) the key in the running gateway and stores it under
// keyring service Project, account POLYPUS_API_KEY. It returns the key name.
func Create(ctx context.Context, cfg Config) (string, error) {
	if strings.TrimSpace(cfg.Project) == "" {
		return "", fmt.Errorf("clientkey: project required")
	}
	container, err := gatewayContainer(ctx, cfg)
	if err != nil {
		return "", err
	}
	verb := "generate"
	if cfg.Rotate {
		verb = "rotate"
	}
	name := cfg.keyName()
	out, err := cfg.docker(ctx, []string{"exec", container, "polypus", "admin-key", verb, "--name", name, "--yes", "--json"})
	if err != nil {
		return "", fmt.Errorf("clientkey: admin-key %s in %s: %w", verb, container, err)
	}
	var res struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(out, &res); err != nil || strings.TrimSpace(res.Key) == "" {
		return "", fmt.Errorf("clientkey: admin-key %s gave no key; run polypus deploy client-key --rotate to replace %s", verb, name)
	}
	if err := secrets.Store(cfg.Keyring, cfg.Project, secrets.ClientKeyName, res.Key); err != nil {
		return "", fmt.Errorf("clientkey: key %s was created but not stored (%w); run polypus deploy client-key --rotate", name, err)
	}
	return name, nil
}

// gatewayContainer finds the environment's running gateway container by its compose
// labels.
func gatewayContainer(ctx context.Context, cfg Config) (string, error) {
	args := []string{"ps"}
	for _, f := range []string{
		"label=com.docker.compose.project=" + cfg.Project,
		"label=com.docker.compose.service=gateway",
		"status=running",
	} {
		args = append(args, "--filter", f)
	}
	out, err := cfg.docker(ctx, append(args, "--format", "{{.Names}}"))
	if err != nil {
		return "", fmt.Errorf("clientkey: find gateway: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("clientkey: no running gateway in compose project %s; run polypus deploy run first", cfg.Project)
}

// Verify checks the gateway at baseURL now needs key: /v1/models answers 401 without
// it and 200 with it.
func Verify(ctx context.Context, client *http.Client, baseURL, key string) error {
	without, err := modelsStatus(ctx, client, baseURL, "")
	if err != nil {
		return err
	}
	if without != http.StatusUnauthorized {
		return fmt.Errorf("clientkey: /v1/models without a key answered %d, want 401", without)
	}
	with, err := modelsStatus(ctx, client, baseURL, key)
	if err != nil {
		return err
	}
	if with != http.StatusOK {
		return fmt.Errorf("clientkey: /v1/models with the key answered %d, want 200", with)
	}
	return nil
}

func modelsStatus(ctx context.Context, client *http.Client, baseURL, key string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/models", nil)
	if err != nil {
		return 0, fmt.Errorf("clientkey: %w", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := outbound.DoOnce(ctx, outbound.DepGateway, client, req)
	if err != nil {
		return 0, fmt.Errorf("clientkey: /v1/models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}
