package deploy

import (
	"io"
	"strings"

	"github.com/behaviorengineering/polypus/internal/deploy/paths"
	"github.com/behaviorengineering/polypus/internal/deploy/progress"
	"github.com/behaviorengineering/polypus/internal/deploy/smoke"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// SmokeRequest is a polypus deploy smoke run as the operator asked for it. A nil
// With* check was left unset, so with an environment the deploy run's choice
// fills it (ResolveSmokeChecks).
type SmokeRequest struct {
	// Deploy is the environment's deploy options; nil when none was found.
	Deploy *Options
	// NoEnvironment is returned when a check needs an environment and Deploy is
	// nil; it should say why none was found.
	NoEnvironment error
	// BaseURL is the gateway; empty uses the environment's.
	BaseURL string
	// EnvDeploy is the dotenv that gates probes by secret; empty uses the
	// environment's .env.deploy.
	EnvDeploy string
	// ChatModel, when set, keeps the direct chat in place of the provider probes.
	ChatModel                                string
	WithRouter, WithSystemOne, WithProviders *bool
	// Out receives the smoke's lines.
	Out io.Writer
}

// ResolveSmoke turns req into the smoke's options, so polypus deploy smoke and
// deploy run agree on which checks run: the environment's checks fill what the
// operator left unset, and the client key comes from the environment's secrets
// (Deploy.Secrets). Without an environment only a check that needs one fails.
func ResolveSmoke(req SmokeRequest) (smoke.Options, error) {
	const op = "deploy.ResolveSmoke"
	o := smoke.Options{
		BaseURL:            req.BaseURL,
		ChatModel:          req.ChatModel,
		WithRouter:         isSet(req.WithRouter),
		WithSystemOne:      isSet(req.WithSystemOne),
		WithProviders:      isSet(req.WithProviders),
		SystemOneRequested: isSet(req.WithSystemOne),
		Out:                req.Out,
	}
	var opts Options
	if req.Deploy != nil {
		opts = *req.Deploy
		key, err := smokeClientKey(opts, progress.New(req.Out))
		if err != nil {
			return o, err
		}
		o.APIKey = key
		gatewayPath, err := paths.ResolveGatewayConfig(opts.GatewayConfig)
		if err != nil {
			return o, err
		}
		checks, err := ResolveSmokeChecks(gatewayPath)
		if err != nil {
			return o, err
		}
		if req.WithProviders == nil && strings.TrimSpace(req.ChatModel) == "" {
			o.WithProviders = checks.Providers
		}
		if req.WithRouter == nil {
			o.WithRouter = checks.Routers
		}
		if req.WithSystemOne == nil {
			o.WithSystemOne = checks.SystemOne
		}
	}
	needsGateway := o.WithProviders || o.WithSystemOne || o.WithRouter
	if req.Deploy == nil && (needsGateway || o.BaseURL == "") {
		if req.NoEnvironment != nil {
			return o, req.NoEnvironment
		}
		return o, derrors.New(derrors.CodeNotFound, op, "no deploy environment found (pass --config)")
	}
	envDeploy := req.EnvDeploy
	if needsGateway {
		var err error
		if o.GatewayConfig, err = paths.ResolveGatewayConfig(opts.GatewayConfig); err != nil {
			return o, err
		}
		if strings.TrimSpace(envDeploy) == "" {
			envDeploy = opts.EnvDeployPath()
		}
	}
	o.EnvDeploy = envDeploy
	if o.BaseURL == "" {
		o.BaseURL = opts.HealthURL
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	return o, nil
}

func isSet(b *bool) bool { return b != nil && *b }
