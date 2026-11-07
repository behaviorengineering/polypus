package deploy

import (
	"context"
	"os"

	"github.com/behaviorengineering/polypus/internal/deploy/images"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// VerifyImages checks the image pins an environment would deploy without deploying:
// the pins name every service the environment runs, each manifest exists in its
// registry, and the release behind the Polypus version finished publishing. With no
// pinned file it checks this build's release pins. ImageProbe and GitHub replace
// the registry and GitHub clients.
func VerifyImages(ctx context.Context, opts Options) error {
	const op = "deploy.VerifyImages"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	fail := func(err error, fallback derrors.Code, step string) error {
		return derrors.Wrap(err, derrors.CodeOr(err, fallback), op, step).With("environment", opts.Name)
	}
	svc, err := Services(opts)
	if err != nil {
		return fail(err, derrors.CodeInvalid, "services")
	}
	path, cleanup, err := verifyImagesFile(opts, svc)
	if err != nil {
		return fail(err, derrors.CodeInvalid, "pins")
	}
	defer cleanup()
	ensure := images.EnsureOptions{Wait: false, Services: svc, Probe: opts.ImageProbe, Out: opts.Out}
	if err := images.EnsurePresent(ctx, path, ensure); err != nil {
		return fail(err, derrors.CodeUnavailable, "registry manifests")
	}
	ver, err := images.PolypusVersionFromEnv(path)
	if err != nil {
		return fail(err, derrors.CodeInvalid, "polypus version")
	}
	wait := images.DefaultWaitOptions()
	wait.GitHub = opts.GitHub
	if err := images.VerifyPublishComplete(ctx, ver, wait); err != nil {
		return fail(err, derrors.CodeUnavailable, "publish-complete "+ver)
	}
	return nil
}

// verifyImagesFile returns the pins file to verify and a cleanup to defer: the
// environment's own file (cleanup does nothing), or this build's release pins
// written to a temporary file (cleanup removes it).
func verifyImagesFile(opts Options, svc images.Services) (path string, cleanup func(), err error) {
	path, pins, err := resolvePins(opts, svc)
	if err != nil || pins == "" {
		return path, func() {}, err
	}
	return writeTempPins(pins)
}

// writeTempPins writes pins to a temporary file and returns it with its removal.
func writeTempPins(pins string) (string, func(), error) {
	const op = "deploy.VerifyImages"
	noop := func() {}
	f, err := os.CreateTemp("", "polypus-images-*.env")
	if err != nil {
		return "", noop, derrors.Wrap(err, derrors.CodeInternal, op, "temp pins")
	}
	name := f.Name()
	remove := func() { _ = os.Remove(name) } // best-effort cleanup of the temp pins file.
	if err := f.Close(); err != nil {
		remove()
		return "", noop, derrors.Wrap(err, derrors.CodeInternal, op, "temp pins")
	}
	if err := images.WritePins(name, pins); err != nil {
		remove()
		return "", noop, err
	}
	return name, remove, nil
}
