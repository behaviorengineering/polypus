package images

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var imageTagPattern = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)(?:-r([1-9][0-9]*))?$`)

// ParsedImageTag is an GHCR pin tag (app semver plus optional rebuild).
type ParsedImageTag struct {
	App     string
	Rebuild int
}

// ParseImageTag parses X.Y.Z or X.Y.Z-rN image tags.
func ParseImageTag(tag string) (ParsedImageTag, error) {
	tag = strings.TrimSpace(tag)
	tag = strings.TrimPrefix(tag, "v")
	m := imageTagPattern.FindStringSubmatch(tag)
	if m == nil {
		return ParsedImageTag{}, fmt.Errorf("images: invalid image tag %q", tag)
	}
	out := ParsedImageTag{App: m[1]}
	if m[2] != "" {
		n, err := strconv.Atoi(m[2])
		if err != nil || n < 1 {
			return ParsedImageTag{}, fmt.Errorf("images: invalid rebuild in tag %q", tag)
		}
		out.Rebuild = n
	}
	return out, nil
}

// AppGitTag returns vX.Y.Z for git and GitHub Release lookups.
func AppGitTag(app string) string {
	app = strings.TrimSpace(app)
	app = strings.TrimPrefix(app, "v")
	return "v" + app
}

// AppVersionFromEnv returns app semver X.Y.Z from POLYPUS_IMAGE (strips -rN).
func AppVersionFromEnv(path string) (string, error) {
	pin, err := PolypusVersionFromEnv(path)
	if err != nil {
		return "", err
	}
	parsed, err := ParseImageTag(pin)
	if err != nil {
		return "", err
	}
	return parsed.App, nil
}
