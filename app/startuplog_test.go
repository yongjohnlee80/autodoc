package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/userdirs"
)

// The startup log is in AutoDoc's folder of the user's state, made if need be, and each run's
// starts empty: what it holds is that run's.
func TestTheStartupLogIsThisRunsAndInTheStateFolder(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("LOCALAPPDATA", state)
	dir, err := userdirs.State(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "autodoc", startupLogName)
	if got, err := startupLogPath(); err != nil || got != want {
		t.Fatalf("startupLogPath = %q, %v; want %q", got, err, want)
	}
	f, err := openStartupLog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("the last run's reason\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	f, err = openStartupLog()
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if b, err := os.ReadFile(want); err != nil || strings.Contains(string(b), "last run") {
		t.Errorf("the log after a new run: %q, %v; want it empty", b, err)
	}
}
