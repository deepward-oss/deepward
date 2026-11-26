package app

import "testing"

func TestMainRunsTheCLI(t *testing.T) {
	if code := Main([]string{"version"}); code != 0 {
		t.Errorf("version: exit %d", code)
	}
	if code := Main([]string{"no-such-command"}); code != 2 {
		t.Errorf("unknown command: exit %d", code)
	}
}
