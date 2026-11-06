// Package images holds the deploy's image policy: verify the pins file, check each
// pinned image is in its registry, wait for the release's image build on GitHub
// Actions when it is not, and gate on the release's publish-complete run.
package images

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/github"
	"github.com/behaviorengineering/polypus/internal/clients/registry"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// EnsureOptions configures registry manifest probes and the optional Actions wait.
type EnsureOptions struct {
	// Token is the GitHub token (else GHCR_TOKEN, GITHUB_TOKEN or GH_TOKEN). The
	// registry probe sends it to ghcr.io only; the Actions wait sends it to GitHub.
	Token       string
	Wait        bool
	WaitTimeout time.Duration
	// Registry checks the manifests; nil builds one from Token.
	Registry *registry.Client
	// GitHub reads the Actions runs during the wait; nil builds one from Token.
	GitHub *github.Client
	// Probe replaces the registry check (tests).
	Probe        func(context.Context, string, string) error
	WaitForBuild func(context.Context, string, WaitOptions) error
	// After Actions success, re-probe the registry on 404 before failing (registry
	// lag). Both are required when Wait is set; DefaultEnsureOptions fills them.
	PostWaitProbeTimeout  time.Duration
	PostWaitProbeInterval time.Duration
	// Services picks the images to probe: Polypus always, Switchyard with routers.
	Services Services
	// Out receives progress lines; nil writes to os.Stdout.
	Out io.Writer
}

// DefaultEnsureOptions returns a deploy's image check: wait for the release
// workflow when a tag is missing, then give the registry two minutes to catch up.
func DefaultEnsureOptions() EnsureOptions {
	return EnsureOptions{
		Wait:                  true,
		WaitTimeout:           DefaultWaitOptions().Timeout,
		PostWaitProbeTimeout:  2 * time.Minute,
		PostWaitProbeInterval: 15 * time.Second,
	}
}

// EnsurePresent probes POLYPUS_IMAGE, and SWITCHYARD_IMAGE when routers run, in images.env.
func EnsurePresent(ctx context.Context, path string, opts EnsureOptions) error {
	const op = "images.EnsurePresent"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if opts.Wait && (opts.PostWaitProbeTimeout <= 0 || opts.PostWaitProbeInterval <= 0) {
		return derrors.New(derrors.CodeInvalid, op, "post-wait probe timeout and interval are required with Wait")
	}
	refs, err := probeRefsFromEnv(path, opts.Services.probeKeys())
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, op, "read pins")
	}
	token := opts.Token
	if token == "" {
		token = registryToken()
	}
	probe := opts.probeFunc(token)

	var missing []string
	for _, ref := range refs {
		if err := probe(ctx, ref, token); err != nil {
			if errors.Is(err, ErrManifestNotFound) {
				missing = append(missing, ref)
				continue
			}
			return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "ensure "+ref)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !opts.Wait {
		return derrors.New(derrors.CodeNotFound, op, "registry manifest missing for "+strings.Join(missing, ", ")+
			" (set GHCR_TOKEN for private packages)")
	}

	ver, err := versionFromRefs(refs)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, op, "image version")
	}
	waitFn := opts.WaitForBuild
	if waitFn == nil {
		waitFn = WaitForImages
	}
	waitOpts := applyWaitDefaults(WaitOptions{Timeout: opts.WaitTimeout, GitHub: opts.GitHub, Token: token, Out: opts.Out})
	if err := waitFn(ctx, ver, waitOpts); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "wait for image build "+ver)
	}
	return probeMissingAfterActions(ctx, missing, probe, token, opts)
}

// probeFunc is opts.Probe, else a manifest probe on opts.Registry or a registry
// client for token.
func (o EnsureOptions) probeFunc(token string) func(context.Context, string, string) error {
	if o.Probe != nil {
		return o.Probe
	}
	reg := o.Registry
	if reg == nil {
		reg = newRegistryClient(token)
	}
	return func(ctx context.Context, ref, _ string) error {
		return probeWith(ctx, reg, ref)
	}
}

func probeMissingAfterActions(
	ctx context.Context,
	missing []string,
	probe func(context.Context, string, string) error,
	token string,
	opts EnsureOptions,
) error {
	const op = "images.EnsurePresent"
	deadline := time.Now().Add(opts.PostWaitProbeTimeout)
	for {
		stillMissing := make([]string, 0, len(missing))
		for _, ref := range missing {
			if err := probe(ctx, ref, token); err != nil {
				if errors.Is(err, ErrManifestNotFound) {
					stillMissing = append(stillMissing, ref)
					continue
				}
				return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "after wait, "+ref)
			}
		}
		if len(stillMissing) == 0 {
			return nil
		}
		missing = stillMissing
		if time.Now().After(deadline) {
			return derrors.New(derrors.CodeNotFound, op,
				"registry manifest still missing after Actions success for "+strings.Join(missing, ", ")).
				With("waited", opts.PostWaitProbeTimeout.String())
		}
		if err := sleepCtx(ctx, opts.PostWaitProbeInterval); err != nil {
			return derrors.Wrap(err, derrors.CodeCanceled, op, "registry catch-up")
		}
	}
}
