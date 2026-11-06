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
	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/behaviorengineering/polypus/internal/deploy/stack"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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
	// HealthURL is the gateway base URL Lock verifies the lock against.
	HealthURL string
	// SkipVerify makes Lock stop after storing the key.
	SkipVerify bool
	// HTTP sends the verify requests; nil uses a client bounded only by ctx.
	HTTP *http.Client
}

// Result is what Lock did: the key's name, and the URL the lock was verified at
// (empty when verification was skipped or failed).
type Result struct {
	Name        string
	VerifiedURL string
}

// Lock creates and stores the key (Create), then, unless SkipVerify, reads it back
// from the keyring and checks the gateway at HealthURL answers 401 without it and
// 200 with it. On a verify failure Result.Name still names the stored key.
func Lock(ctx context.Context, cfg Config) (Result, error) {
	const op = "clientkey.Lock"
	name, err := Create(ctx, cfg)
	if err != nil {
		return Result{}, err
	}
	res := Result{Name: name}
	if cfg.SkipVerify {
		return res, nil
	}
	url := strings.TrimRight(strings.TrimSpace(cfg.HealthURL), "/")
	if url == "" {
		return res, derrors.New(derrors.CodeInvalid, op, "health URL required to verify the lock (or set SkipVerify)")
	}
	key, err := secrets.Lookup(cfg.Keyring, cfg.Project, secrets.ClientKeyName)
	if err != nil {
		return res, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotFound), op, "read stored key").With("keyring_service", cfg.Project)
	}
	client := cfg.HTTP
	if client == nil {
		client = &http.Client{}
	}
	if err := Verify(ctx, client, url, key); err != nil {
		return res, err
	}
	res.VerifiedURL = url
	return res, nil
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
	return execx.StdoutContext(ctx, "docker", args, nil)
}

// Create generates (or rotates) the key in the running gateway and stores it under
// keyring service Project, account POLYPUS_API_KEY. It returns the key name.
func Create(ctx context.Context, cfg Config) (string, error) {
	const op = "clientkey.Create"
	if ctx == nil {
		return "", derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if strings.TrimSpace(cfg.Project) == "" {
		return "", derrors.New(derrors.CodeInvalid, op, "project required")
	}
	container, err := gatewayContainer(ctx, cfg)
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "find gateway").With("project", cfg.Project)
	}
	verb := "generate"
	if cfg.Rotate {
		verb = "rotate"
	}
	name := cfg.keyName()
	// admin-key generate and rotate are not idempotent, so they run once.
	out, err := cfg.docker(ctx, []string{"exec", container, "polypus", "admin-key", verb, "--name", name, "--yes", "--json"})
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeUnavailable, op, fmt.Sprintf("admin-key %s in %s", verb, container))
	}
	var res struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, op,
			fmt.Sprintf("admin-key %s output is not JSON (is the gateway image older than this CLI?); a key may exist: run polypus deploy client-key --rotate to replace %s", verb, name))
	}
	if strings.TrimSpace(res.Key) == "" {
		return "", derrors.New(derrors.CodeInternal, op,
			fmt.Sprintf("admin-key %s gave no key; run polypus deploy client-key --rotate to replace %s", verb, name))
	}
	if err := secrets.Store(cfg.Keyring, cfg.Project, secrets.ClientKeyName, res.Key); err != nil {
		return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), op,
			fmt.Sprintf("key %s was created but not stored; run polypus deploy client-key --rotate", name))
	}
	return name, nil
}

// gatewayContainer finds the environment's running gateway container by its compose
// labels.
func gatewayContainer(ctx context.Context, cfg Config) (string, error) {
	args := []string{"ps"}
	for _, f := range []string{
		"label=com.docker.compose.project=" + cfg.Project,
		"label=com.docker.compose.service=" + stack.ServiceGateway,
		"status=running",
	} {
		args = append(args, "--filter", f)
	}
	args = append(args, "--format", "{{.Names}}")
	// docker ps only reads, so a docker daemon blip is retried like compose up.
	var out []byte
	err := outbound.Run(ctx, outbound.DepDockerDaemon, func() error {
		var runErr error
		out, runErr = cfg.docker(ctx, args)
		if runErr == nil {
			return nil
		}
		if compose.DockerExecRetryable(runErr) {
			return outbound.Retry(runErr)
		}
		return outbound.Abort(runErr)
	})
	if err != nil {
		return "", fmt.Errorf("clientkey: find gateway: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			return name, nil
		}
	}
	return "", derrors.New(derrors.CodeNotFound, "clientkey.Create",
		"no running gateway in compose project "+cfg.Project+"; run polypus deploy run first")
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
