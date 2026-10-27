package tui

import (
	"strings"
	"testing"
)

// The entries whose keys run their App.* twins, run by their ids as Lua's autodoc.run and a
// keymap's binding run them: help.about opens About, help.keys the help, and find.next and
// find.previous, with nothing found yet, say there is nothing to find again.
func TestBuiltinEntriesRunByTheirIDs(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	run := func(id string) {
		t.Helper()
		if err := onLoop(r, func() error { return r.h.cat.run(id, nil) }); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	status := func() string {
		return onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw })
	}
	for _, id := range []string{"find.next", "find.previous"} {
		onLoop(r, func() bool { r.h.say(""); return true })
		run(id)
		if s := status(); !strings.Contains(s, "nothing to find again") {
			t.Errorf("%s with nothing found: the status says %q", id, s)
		}
	}
	run("help.about")
	r.s.WaitForText(t, "about autodoc")
	r.keys(t, esc())
	r.s.WaitFor(t, "About closed", func(sc string) bool { return !strings.Contains(sc, "about autodoc") })
	run("help.keys")
	r.s.WaitForText(t, "the leader card, in Normal mode") // the help's first lines
}
