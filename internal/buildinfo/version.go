package buildinfo

import (
	"runtime/debug"
	"strings"

	"golang.org/x/mod/semver"
)

// Version is the gateway/CLI build identity (GoReleaser ldflags or "dev").
// A go install or go run of a release tag has no ldflags, so it falls back to the
// module version Go records in the binary.
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v, ok := releaseVersion(info.Main.Version); ok {
			Version = v
		}
	}
}

// releaseVersion turns a release tag such as v0.4.0 into the GoReleaser form 0.4.0.
// Pseudo-versions, pre-releases and "(devel)" are not releases, so they report false.
func releaseVersion(module string) (string, bool) {
	if !semver.IsValid(module) || semver.Canonical(module) != module || semver.Prerelease(module) != "" {
		return "", false
	}
	return strings.TrimPrefix(module, "v"), true
}
