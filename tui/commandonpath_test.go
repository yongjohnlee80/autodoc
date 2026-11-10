package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
)

// A bare name is judged only on a PATH of absolute folders: a relative entry (or an empty one, the
// current folder) is the agent's folder's, its process's working directory, which the check does not
// run in, so there the shell decides. On a PATH of absolute folders a name in none of them is refused.
func TestTheCommandCheckLeavesARelativePathEntryToTheShell(t *testing.T) {
	abs := t.TempDir()
	sep := string(os.PathListSeparator)
	for _, path := range []string{"bin" + sep + abs, abs + sep, "." + sep + abs} {
		env := procenv.Env(nil).With("PATH=" + path)
		if err := commandOnPath(usershell.Commands(env), "autodoc-no-such-agent", env); err != nil {
			t.Errorf("PATH %q: %v; want it left to the shell", path, err)
		}
	}
	env := procenv.Env(nil).With("PATH=" + abs + sep + filepath.Join(abs, "more"))
	if err := commandOnPath(usershell.Commands(env), "autodoc-no-such-agent", env); err == nil {
		t.Error("a name on no folder of an absolute PATH was not refused")
	}
}
