package app

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui"
)

// TestARelaunchExecsThisProgramsPathWithItsArguments: a run that ended with tui.ErrRelaunch, joined
// with others or not, execs the binary at this program's path with the same arguments and
// environment; any other ending passes through untouched.
func TestARelaunchExecsThisProgramsPathWithItsArguments(t *testing.T) {
	was := execProgram
	defer func() { execProgram = was }()
	var gotPath string
	var gotArgs []string
	execProgram = func(path string, args, env []string) error {
		gotPath, gotArgs = path, args
		return errors.New("stand-in: not replaced")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range []error{tui.ErrRelaunch, errors.Join(tui.ErrRelaunch, errors.New("a close failed")), fmt.Errorf("wrapped: %w", tui.ErrRelaunch)} {
		gotPath, gotArgs = "", nil
		err := afterRun(runErr)
		if gotPath != exe || !slices.Equal(gotArgs, os.Args) {
			t.Fatalf("%v: exec'd %q %q, want %q %q", runErr, gotPath, gotArgs, exe, os.Args)
		}
		if err == nil {
			t.Fatalf("%v: an exec that failed returned no error", runErr)
		}
	}
	other := errors.New("the UI failed")
	for _, runErr := range []error{nil, other} {
		gotPath = ""
		if err := afterRun(runErr); err != runErr || gotPath != "" {
			t.Fatalf("%v: afterRun %v, exec'd %q", runErr, err, gotPath)
		}
	}
}
