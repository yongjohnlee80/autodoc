package tui

import (
	"errors"
	"slices"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// maximizeHost is a panel host as panelRows asks it: whether the panel is maximized, its toggle
// (failing with err), and what it is given to keep.
type maximizeHost struct {
	maximized bool
	err       error
	toggled   []string
	kept      []error
}

func (m *maximizeHost) PanelMaximized(string) bool { return m.maximized }
func (m *maximizeHost) ToggleMaximize(p string) error {
	m.toggled = append(m.toggled, p)
	return m.err
}
func (m *maximizeHost) Keep(err error) { m.kept = append(m.kept, err) }

// runRow chooses the row id of rows, for no tab.
func runRow(t *testing.T, rows []widget.MenuItemModel, id string) {
	t.Helper()
	row, ok := menuRow(rows, id)
	if !ok {
		t.Fatalf("no %s in %v", id, rowIDs(rows))
	}
	row.Action.(widget.TerminalTabsMenuAction).Run("")
}

// TestPanelRowsOfferOnlyWhatTheHostHas: a host with no look and no maximize gets the separator and
// Hide alone, and Hide is the panel's own toggle.
func TestPanelRowsOfferOnlyWhatTheHostHas(t *testing.T) {
	hid := 0
	rows := panelRows(struct{}{}, "terminal", func() { hid++ })
	if got := rowIDs(rows); !slices.Equal(got, []string{"tabs.panel", "tabs.hide"}) {
		t.Fatalf("a bare host's rows: %v", got)
	}
	runRow(t, rows, "tabs.hide")
	if hid != 1 {
		t.Errorf("Hide ran the toggle %d times", hid)
	}
}

// TestPanelRowsMaximizeTheirOwnPanel: Maximize toggles the menu's own panel; maximized, the row reads
// Restore (s); a toggle that fails is kept, not lost.
func TestPanelRowsMaximizeTheirOwnPanel(t *testing.T) {
	m := &maximizeHost{}
	rows := panelRows(m, "agent", func() {})
	if row, _ := menuRow(rows, "tabs.maximize"); row.Label != "Maximize" || row.Hotkey != 'm' {
		t.Errorf("restored, the row reads %q (%q)", row.Label, row.Hotkey)
	}
	runRow(t, rows, "tabs.maximize")
	if !slices.Equal(m.toggled, []string{"agent"}) || len(m.kept) != 0 {
		t.Fatalf("toggled %v, kept %v", m.toggled, m.kept)
	}

	m.maximized, m.err = true, errors.New("no such drawer")
	rows = panelRows(m, "agent", func() {})
	if row, _ := menuRow(rows, "tabs.maximize"); row.Label != "Restore" || row.Hotkey != 's' {
		t.Errorf("maximized, the row reads %q (%q)", row.Label, row.Hotkey)
	}
	runRow(t, rows, "tabs.maximize")
	if len(m.kept) != 1 || !errors.Is(m.kept[0], m.err) {
		t.Errorf("a failed toggle kept %v", m.kept)
	}
}
