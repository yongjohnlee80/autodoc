//go:build linux || darwin

package usershell

import (
	"reflect"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

func shellsHere(*testing.T) []Shell { return []Shell{Commands(nil)} }

// On Unix a command line is /bin/sh's, and a terminal starts $SHELL, else /bin/sh.
func TestTheShellsOnUnix(t *testing.T) {
	if s := Commands(procenv.Env{"SHELL=/bin/zsh"}); s.Program != "/bin/sh" || s.kind != posix {
		t.Errorf("Commands = %+v, want /bin/sh whatever $SHELL is", s)
	}
	if got := Interactive(procenv.Env{"SHELL=/bin/zsh"}); !reflect.DeepEqual(got, []string{"/bin/zsh"}) {
		t.Errorf("Interactive with $SHELL = %q", got)
	}
	if got := Interactive(procenv.Env{}); !reflect.DeepEqual(got, []string{"/bin/sh"}) {
		t.Errorf("Interactive without $SHELL = %q", got)
	}
}
