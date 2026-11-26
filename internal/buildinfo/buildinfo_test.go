package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef0123"}, {Key: "vcs.time", Value: "2026-11-26T10:00:00Z"}, {Key: "vcs.modified", Value: "false"}}}
	if v, c, d := resolve("dev", "unknown", "unknown", installed); v != "v0.1.0" || c != "0123456789ab" || d != "2026-11-26T10:00:00Z" {
		t.Errorf("go install: %s %s %s", v, c, d)
	}
	if v, c, d := resolve("0.2.0", "abc1234", "2026-12-01", installed); v != "0.2.0" || c != "abc1234" || d != "2026-12-01" {
		t.Errorf("ldflags win: %s %s %s", v, c, d)
	}
	local := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}}
	if v, c, d := resolve("dev", "unknown", "unknown", local); v != "dev-dirty" || c != "unknown" || d != "unknown" {
		t.Errorf("local build: %s %s %s", v, c, d)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"v0.1.0": "0.1.0", "0.1.0": "0.1.0", "v0.1.1-3-gabc": "0.1.1-3-gabc", "dev": "dev", "v": "v", "version": "version"} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
