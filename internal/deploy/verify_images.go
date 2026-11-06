package deploy

import (
	"context"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/deploy/images"
	"github.com/behaviorengineering/polypus/internal/deploy/stack"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// VerifyImages checks the image pins an environment would deploy without deploying:
// the pins name every service the environment runs, each manifest exists in its
// registry, and the release behind the Polypus version finished publishing. With no
// pinned file it checks this build's release pins.
func VerifyImages(ctx context.Context, opts Options) error {
	const op = "deploy.VerifyImages"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	svc, err := Services(opts)
	if err != nil {
		return err
	}
	path, cleanup, err := verifyImagesFile(opts, svc)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := images.EnsurePresent(ctx, path, images.EnsureOptions{Wait: false, Services: svc}); err != nil {
		return err
	}
	ver, err := images.PolypusVersionFromEnv(path)
	if err != nil {
		return err
	}
	return images.VerifyPublishComplete(ctx, ver, images.DefaultWaitOptions())
}

// verifyImagesFile returns the pins file to verify and a cleanup to defer: the
// environment's own file (cleanup does nothing), or this build's release pins
// written to a temporary file (cleanup removes it).
func verifyImagesFile(opts Options, svc images.Services) (path string, cleanup func(), err error) {
	const op = "deploy.VerifyImages"
	noop := func() {}
	if strings.TrimSpace(opts.PinsFile) != "" {
		return opts.PinsFile, noop, images.Verify(opts.PinsFile, svc)
	}
	pins, err := images.VersionPins(stack.ImagePins(), opts.Version)
	if err != nil {
		return "", noop, err
	}
	if err := images.VerifyPins(pins, "the embedded images.env", svc); err != nil {
		return "", noop, err
	}
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
