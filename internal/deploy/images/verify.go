package images

import (
	"maps"
	"os"
	"slices"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// Verify checks images.env for the pins svc needs, and that every pin names a tag
// or digest other than latest.
func Verify(path string, svc Services) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the operator's own images file
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, "images.Verify", "read "+path)
	}
	return VerifyPins(string(data), path, svc)
}

// VerifyPins checks pins (images.env content; path names it in errors) for the
// pins svc needs, and that every pin names a tag or digest other than latest: an
// untagged ref pulls latest too.
func VerifyPins(pins, path string, svc Services) error {
	const op = "images.VerifyPins"
	found := parseEnv(pins)
	for _, key := range svc.pinKeys() {
		if _, ok := found[key]; !ok {
			return derrors.New(derrors.CodeInvalid, op, "missing "+key+" in "+path)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(found)) {
		val := found[key]
		if !pinned(val) {
			return derrors.New(derrors.CodeInvalid, op, key+" must pin a tag or digest, not latest (got "+val+")").
				With("path", path)
		}
	}
	return nil
}

// pinned reports whether ref names a digest, or a tag other than latest. The tag
// is what follows the last ":" after the last "/", so a registry port is not a tag.
func pinned(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	name := ref[strings.LastIndex(ref, "/")+1:]
	_, tag, ok := strings.Cut(name, ":")
	return ok && tag != "" && tag != "latest"
}
