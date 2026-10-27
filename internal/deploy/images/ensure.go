package images

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultPostWaitProbeTimeout  = 2 * time.Minute
	defaultPostWaitProbeInterval = 15 * time.Second
)

// EnsureOptions configures GHCR manifest probes and optional Actions wait.
type EnsureOptions struct {
	Token        string
	Wait         bool
	WaitTimeout  time.Duration
	Probe        func(context.Context, string, string) error
	WaitForBuild func(context.Context, string, WaitOptions) error
	// After Actions success, re-probe GHCR on 404 before failing (GHCR lag).
	PostWaitProbeTimeout  time.Duration
	PostWaitProbeInterval time.Duration
}

// EnsurePresent probes POLYPUS_IMAGE and SWITCHYARD_IMAGE in images.env.
func EnsurePresent(ctx context.Context, path string, opts EnsureOptions) error {
	if ctx == nil {
		return fmt.Errorf("images: context required")
	}
	refs, err := probeRefsFromEnv(path)
	if err != nil {
		return err
	}
	probe := opts.Probe
	if probe == nil {
		probe = ProbeManifest
	}
	token := opts.Token
	if token == "" {
		token = registryToken()
	}

	var missing []string
	for _, ref := range refs {
		if err := probe(ctx, ref, token); err != nil {
			if errors.Is(err, ErrManifestNotFound) {
				missing = append(missing, ref)
				continue
			}
			return fmt.Errorf("images: ensure %s: %w", ref, err)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !opts.Wait {
		return fmt.Errorf("images: GHCR manifest missing for %s (set GHCR_TOKEN for private packages)", strings.Join(missing, ", "))
	}

	ver, err := versionFromRefs(refs)
	if err != nil {
		return err
	}
	waitFn := opts.WaitForBuild
	if waitFn == nil {
		waitFn = WaitForImages
	}
	waitOpts := applyWaitDefaults(WaitOptions{Timeout: opts.WaitTimeout})
	if opts.WaitTimeout > 0 {
		waitOpts.Timeout = opts.WaitTimeout
	}
	if err := waitFn(ctx, ver, waitOpts); err != nil {
		return err
	}
	return probeMissingAfterActions(ctx, missing, probe, token, opts)
}

func probeMissingAfterActions(
	ctx context.Context,
	missing []string,
	probe func(context.Context, string, string) error,
	token string,
	opts EnsureOptions,
) error {
	interval := opts.PostWaitProbeInterval
	if interval <= 0 {
		interval = defaultPostWaitProbeInterval
	}
	timeout := opts.PostWaitProbeTimeout
	if timeout <= 0 {
		timeout = defaultPostWaitProbeTimeout
	}
	deadline := time.Now().Add(timeout)

	for {
		stillMissing := make([]string, 0, len(missing))
		for _, ref := range missing {
			if err := probe(ctx, ref, token); err != nil {
				if errors.Is(err, ErrManifestNotFound) {
					stillMissing = append(stillMissing, ref)
					continue
				}
				return fmt.Errorf("images: after wait, %s: %w", ref, err)
			}
		}
		if len(stillMissing) == 0 {
			return nil
		}
		missing = stillMissing
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"images: GHCR manifest still missing after Actions success for %s (waited %s for registry catch-up)",
				strings.Join(missing, ", "),
				timeout,
			)
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return err
		}
	}
}
