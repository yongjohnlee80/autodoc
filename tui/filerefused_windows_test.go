//go:build windows

package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// TestAnOutsideFileHeldOpenSaysItIsInUse: a file outside every workspace that another handle holds
// open (os.Open, which shares no delete) through a save: the save fails, and the status line says
// the file is in use by another program, never that this AutoDoc is remote.
func TestAnOutsideFileHeldOpenSaysItIsInUse(t *testing.T) {
	t.Parallel()
	r, _, _, abs := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	r.typeInEditor(t, "more ")
	held, err := os.Open(abs)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	r.keys(t, decltest.Ctrl('s'))
	r.waitStatus(t, "the file is in use by another program: try again in a moment")
	if s := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw }); strings.Contains(s, "remote") {
		t.Fatalf("the status says %q: a file in use read as a remote daemon", s)
	}
}
