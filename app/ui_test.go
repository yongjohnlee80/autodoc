package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/term"
)

// TestTheUIWantsATerminal: --ui with a config it can load, and no workspace asked for, gets as far as
// opening the terminal — with this platform's terminal options — and refuses a stdin that is not one,
// saying so, rather than drawing into a pipe.
func TestTheUIWantsATerminal(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "config.toml")
	body := "[server]\nsocket = \"" + filepath.Join(short(t), "a.sock") + "\"\nstate_dir = \"" + filepath.Join(dir, "state") + "\"\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe() // never the developer's terminal, whatever go test inherited
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	stdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = stdin }()

	err = runUI(context.Background(), conf, "", "", testOptions)
	if !errors.Is(err, term.ErrNotTerminal) || !strings.Contains(err.Error(), "cannot open the terminal") {
		t.Fatalf("runUI with a pipe for stdin: err = %v, want it to refuse a non-terminal", err)
	}
}
