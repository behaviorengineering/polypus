package images

import (
	"regexp"
	"strconv"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

var imageTagPattern = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)(?:-r([1-9][0-9]*))?$`)

// parsedImageTag is a GHCR pin tag (app semver plus optional rebuild).
type parsedImageTag struct {
	App     string
	Rebuild int
}

// parseImageTag parses X.Y.Z or X.Y.Z-rN image tags.
func parseImageTag(tag string) (parsedImageTag, error) {
	const op = "images.parseImageTag"
	tag = strings.TrimSpace(tag)
	tag = strings.TrimPrefix(tag, "v")
	m := imageTagPattern.FindStringSubmatch(tag)
	if m == nil {
		return parsedImageTag{}, derrors.New(derrors.CodeInvalid, op, "invalid image tag "+strconv.Quote(tag))
	}
	out := parsedImageTag{App: m[1]}
	if m[2] != "" {
		n, err := strconv.Atoi(m[2])
		if err != nil || n < 1 {
			return parsedImageTag{}, derrors.New(derrors.CodeInvalid, op, "invalid rebuild in tag "+strconv.Quote(tag))
		}
		out.Rebuild = n
	}
	return out, nil
}

// appGitTag returns vX.Y.Z for git and GitHub Release lookups.
func appGitTag(app string) string {
	app = strings.TrimSpace(app)
	app = strings.TrimPrefix(app, "v")
	return "v" + app
}
