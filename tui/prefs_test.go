package tui

import (
	"context"
	"strings"
	"testing"
)

// TestTheMenuBarAndTheStatusLineShowByDefault (v0.3.3): with nothing stored, in the terminal and
// the GUI, the menu bar does not hide and the status line shows; a stored choice of either still
// wins.
func TestTheMenuBarAndTheStatusLineShowByDefault(t *testing.T) {
	for _, gui := range []bool{false, true} {
		if p := defaultPrefsFor(gui); p.menuHidden || !p.statusOn {
			t.Errorf("gui %v: a new user's menu bar hidden %v, status line shown %v; want shown, shown", gui, p.menuHidden, p.statusOn)
		}
		var p prefs
		readRows(&p, map[string]any{prefMenuHide: "true", prefStatus: "false"}, legacyPrefRows(nil, gui))
		if !p.menuHidden || p.statusOn {
			t.Errorf("gui %v: stored autohide true and status false read as hidden %v, shown %v", gui, p.menuHidden, p.statusOn)
		}
	}
}

// TestANewUsersWindowShowsTheMenuBarAndTheStatusLine: a store with neither preference: the menu
// bar is drawn on the top row and stays (no auto-hide, the editor alone in the dock), the status
// line on the bottom row, and the start writes neither default to the store.
func TestANewUsersWindowShowsTheMenuBarAndTheStatusLine(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{newUser: true})
	r := attached(t, d)
	r.s.WaitFor(t, "the menu bar on top, the status line at the bottom", func(sc string) bool {
		rows := strings.Split(sc, "\n")
		return strings.Contains(rows[0], "File") && strings.HasPrefix(rows[len(rows)-1], "NORMAL")
	})
	if r.menuAutoHides() {
		t.Error("a new user's menu bar auto-hides")
	}
	m, err := d.db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{prefMenuHide, prefStatus} {
		if v, ok := m[k]; ok {
			t.Errorf("the start wrote %s = %q: a default is never stored", k, v)
		}
	}
}

// TestAStoredChoiceStillHidesThem: a user who chose an auto-hiding menu bar and no status line keeps
// them so: the top row is the page's, and NORMAL is on no row.
func TestAStoredChoiceStillHidesThem(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{newUser: true,
		prefs: map[string]string{prefMenuHide: "true", prefStatus: "false"}})
	r := attached(t, d)
	if !r.menuAutoHides() {
		t.Error("a stored auto-hide is not honoured")
	}
	r.s.WaitFor(t, "no menu bar, no status line", func(sc string) bool {
		rows := strings.Split(sc, "\n")
		return !strings.Contains(rows[0], "File") && !strings.Contains(sc, "NORMAL")
	})
}
