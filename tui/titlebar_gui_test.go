//go:build gui

package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// The layout with the window's own title bar is sound: golib's TitleBar is declared for it, so the
// program builds and every QML file checks.
func TestEveryQMLFileIsSoundWithTheWindowsOwnTitleBar(t *testing.T) {
	t.Parallel()
	opt := Options{GUI: true, TitleBar: true}
	decltest.Check(t, newHost(NewSession("unused", nil), opt).options(opt)...)
}

// firstRow is the screen's top row, its spaces trimmed.
func firstRow(sc string) string {
	row, _, _ := strings.Cut(sc, "\n")
	return strings.TrimSpace(row)
}

// A window that opened without the system's title bar has AutoDoc's own at the very top, above the
// menu bar: here, where no native view is painted, its terminal row with the title. A window with
// the system's title bar has none.
func TestTheWindowsOwnTitleBarIsAtTheTop(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: true, TitleBar: true})
	r.connected(t)
	r.s.WaitFor(t, "the title bar on the top row", func(sc string) bool { return firstRow(sc) == "AutoDoc" })

	plain := runTUI(t, NewSession(d.sock, nil), Options{GUI: true})
	plain.connected(t)
	if row := firstRow(plain.s.String()); row == "AutoDoc" {
		t.Fatalf("a window with the system's title bar drew its own: top row %q", row)
	}
}
