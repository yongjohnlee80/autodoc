package app

import (
	"errors"
	"fmt"
	"os"

	"github.com/yongjohnlee80/autodoc/internal/proctree"
	"github.com/yongjohnlee80/autodoc/tui"
)

// RELAUNCH — About's Update, then Restart now
// (docs/decisions/install.md#abouts-update-and-the-restart): the UI has quit with tui.ErrRelaunch,
// and the binary at this program's path, which the update script replaced, starts in this process's
// place with the same arguments and environment. On Linux the path is read back as it is now
// (os.Executable drops /proc's " (deleted)"), so it's the new binary. On Windows the script moved the
// running binary aside, and this process's path names the new one.

// execProgram replaces this process; tests stand in for it.
var execProgram = proctree.Replace

// afterRun is what a UI's run ends with: its error, unless it asked to relaunch.
func afterRun(err error) error {
	if !errors.Is(err, tui.ErrRelaunch) {
		return err
	}
	exe, xerr := os.Executable()
	if xerr != nil {
		return fmt.Errorf("starting the updated AutoDoc: %w", xerr)
	}
	if xerr := execProgram(exe, os.Args, os.Environ()); xerr != nil {
		return fmt.Errorf("starting the updated AutoDoc (%s): %w", exe, xerr)
	}
	return nil // not reached: exec replaced the process
}
