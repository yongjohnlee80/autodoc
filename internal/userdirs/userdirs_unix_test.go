//go:build linux || darwin

package userdirs

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// The state folder follows XDG's rule: $XDG_STATE_HOME when it is absolute, a relative or empty one
// ignored, and then ~/.local/state; an Env of its own is read, and no home is an error.
func TestTheStateFolderFollowsXDG(t *testing.T) {
	for _, c := range []struct {
		env  procenv.Env
		want string
	}{
		{procenv.Env{"HOME=/home/u", "XDG_STATE_HOME=/xdg/state"}, "/xdg/state"},
		{procenv.Env{"HOME=/home/u", "XDG_STATE_HOME=relative/state"}, "/home/u/.local/state"},
		{procenv.Env{"HOME=/home/u", "XDG_STATE_HOME="}, "/home/u/.local/state"},
		{procenv.Env{"HOME=/home/u"}, "/home/u/.local/state"},
	} {
		if got, err := State(c.env); err != nil || got != c.want {
			t.Errorf("State(%q) = %q, %v; want %q", c.env, got, err, c.want)
		}
	}
	if got, err := State(procenv.Env{}); err == nil {
		t.Errorf("State with no home = %q, want an error", got)
	}
}
