// Package buildinfo holds version metadata. Release builds inject it with
// -ldflags (see Makefile and .goreleaser.yaml); other builds, such as
// `go install github.com/deepward-oss/deepward/cmd/deepward@v0.1.0`, read it
// from the module and VCS information the Go toolchain embeds.
package buildinfo

import (
	"runtime/debug"
)

// These values are overridden by the release build.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		Version, Commit, Date = resolve(Version, Commit, Date, info)
	}
	Version = Normalize(Version)
}

// resolve fills values that were not injected with -ldflags from the
// embedded build information.
func resolve(version, commit, date string, info *debug.BuildInfo) (string, string, string) {
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if commit == "unknown" && s.Value != "" {
				commit = s.Value
				if len(commit) > 12 {
					commit = commit[:12]
				}
			}
		case "vcs.time":
			if date == "unknown" && s.Value != "" {
				date = s.Value
			}
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if version == "dev" && modified {
		version = "dev-dirty"
	}
	return version, commit, date
}

// Normalize drops the "v" of a Git tag, so "v0.1.0" and "0.1.0" read the
// same: Deepward prints versions without the prefix.
func Normalize(v string) string {
	if len(v) > 1 && v[0] == 'v' && v[1] >= '0' && v[1] <= '9' {
		return v[1:]
	}
	return v
}
