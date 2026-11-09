//go:build linux || darwin

package install_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/install"
)

// TestThePathFollowsXDGStateHome: $XDG_STATE_HOME when it's absolute, else ~/.local/state, as the
// scripts resolve it.
func TestThePathFollowsXDGStateHome(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	if p, err := install.Path(); err != nil || p != "/xdg/state/autodoc/install.toml" {
		t.Fatalf("XDG_STATE_HOME set: %q, %v", p, err)
	}
	for _, v := range []string{"", "relative/state"} {
		t.Setenv("XDG_STATE_HOME", v)
		if p, err := install.Path(); err != nil || p != "/home/u/.local/state/autodoc/install.toml" {
			t.Fatalf("XDG_STATE_HOME %q: %q, %v", v, p, err)
		}
	}
}
