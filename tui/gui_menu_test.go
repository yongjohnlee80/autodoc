//go:build gui

package tui

import (
	"strings"
	"testing"

	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// Under --gui's style, SPC . opens the gui Editor's right-click menu, and the menu has the
// keyboard: Escape closes it, rather than reaching the page the leader card gave the keyboard back to.
func TestTheGUISpacePeriodMenuHasTheKeyboard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(guidecl.Native())}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.leader(t, '.')
	r.s.WaitForText(t, "View diagram")
	r.keys(t, esc())
	r.s.WaitFor(t, "the menu closed by Escape", func(sc string) bool { return !strings.Contains(sc, "View diagram") })
}
