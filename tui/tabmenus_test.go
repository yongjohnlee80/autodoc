package tui

import (
	"errors"
	"slices"
	"strings"
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

// TestPanelRowsOfferOnlyWhatTheHostHas: a host with no look and no maximize gets the separator,
// Resume and Hide alone, and Hide is the panel's own toggle.
func TestPanelRowsOfferOnlyWhatTheHostHas(t *testing.T) {
	hid := 0
	rows := panelRows(struct{}{}, "terminal", &modelPanel{}, "", func() { hid++ })
	if got := rowIDs(rows); !slices.Equal(got, []string{"tabs.panel", "tabs.resume", "tabs.hide"}) {
		t.Fatalf("a bare host's rows: %v", got)
	}
	runRow(t, rows, "tabs.hide")
	if hid != 1 {
		t.Errorf("Hide ran the toggle %d times", hid)
	}
}

// TestPanelRowsMaximizeTheirOwnPanel: Maximize toggles the menu's own panel; maximized, the row reads
// Restore (s); a toggle that fails is kept, not lost, named for what failed.
func TestPanelRowsMaximizeTheirOwnPanel(t *testing.T) {
	m := &maximizeHost{}
	rows := panelRows(m, "agent", &modelPanel{}, "", func() {})
	if row, _ := menuRow(rows, "tabs.maximize"); row.Label != "Maximize" || row.Hotkey != 'm' {
		t.Errorf("restored, the row reads %q (%q)", row.Label, row.Hotkey)
	}
	runRow(t, rows, "tabs.maximize")
	if !slices.Equal(m.toggled, []string{"agent"}) || len(m.kept) != 0 {
		t.Fatalf("toggled %v, kept %v", m.toggled, m.kept)
	}

	m.maximized, m.err = true, errors.New("no such drawer")
	rows = panelRows(m, "agent", &modelPanel{}, "", func() {})
	if row, _ := menuRow(rows, "tabs.maximize"); row.Label != "Restore" || row.Hotkey != 's' {
		t.Errorf("maximized, the row reads %q (%q)", row.Label, row.Hotkey)
	}
	runRow(t, rows, "tabs.maximize")
	if len(m.kept) != 1 || !errors.Is(m.kept[0], m.err) || !strings.HasPrefix(m.kept[0].Error(), "Restore the agent panel: ") {
		t.Errorf("a failed toggle kept %v, not naming what failed", m.kept)
	}
}

// lookHost is a panel host with Theme and font…, failing with err.
type lookHost struct {
	err    error
	opened int
	kept   []error
}

func (l *lookHost) OpenTerminalLook() error { l.opened++; return l.err }
func (l *lookHost) Keep(err error)          { l.kept = append(l.kept, err) }

// TestPanelRowsKeepARefusedLook: Theme and font… opens the host's look; refused, the refusal is
// kept, named for what failed, not lost.
func TestPanelRowsKeepARefusedLook(t *testing.T) {
	l := &lookHost{}
	runRow(t, panelRows(l, "terminal", &modelPanel{}, "", func() {}), "tabs.look")
	if l.opened != 1 || len(l.kept) != 0 {
		t.Fatalf("opened %d, kept %v", l.opened, l.kept)
	}
	l.err = errors.New("views are not attached")
	runRow(t, panelRows(l, "terminal", &modelPanel{}, "", func() {}), "tabs.look")
	if len(l.kept) != 1 || !errors.Is(l.kept[0], l.err) || !strings.HasPrefix(l.kept[0].Error(), "Theme and font… for the terminal panel: ") {
		t.Errorf("a refused look kept %v, not naming what failed", l.kept)
	}
}

// scaleHost is a panel host whose window draws the shells' text at pct (drawn), stepping it by the
// steps' list as the Host does, failing with err.
type scaleHost struct {
	pct   int
	drawn bool
	err   error
	kept  []error
}

func (s *scaleHost) PanelTextScale() (int, bool) { return s.pct, s.drawn }
func (s *scaleHost) StepPanelTextScale(dir int) error {
	if s.err != nil {
		return s.err
	}
	s.pct = agentScales[slices.Index(agentScales, s.pct)+dir]
	return nil
}
func (s *scaleHost) Keep(err error) { s.kept = append(s.kept, err) }

// TestPanelRowsStepTheShellsTextSize: in a window, a panel's menu has Text size › (x): the share now,
// then Smaller (s) and Larger (r), a step of 10% each, each disabled at its end of 50–150%; a
// terminal's has none (its host's font is its own); a refused step is kept, named for what failed.
func TestPanelRowsStepTheShellsTextSize(t *testing.T) {
	if _, ok := menuRow(panelRows(&scaleHost{pct: 90}, "agent", &modelPanel{}, "", func() {}), "tabs.text"); ok {
		t.Fatal("a terminal's panel menu offers Text size")
	}
	s := &scaleHost{pct: 90, drawn: true}
	rows := panelRows(s, "terminal", &modelPanel{}, "", func() {})
	sub, ok := menuRow(rows, "tabs.text")
	if !ok || sub.Label != "Text size" || sub.Hotkey != 'x' {
		t.Fatalf("the window's rows %v: Text size %q (%q)", rowIDs(rows), sub.Label, sub.Hotkey)
	}
	if now, _ := menuRow(rows, "tabs.text.now"); now.Label != "90%" || now.Enabled {
		t.Errorf("the share now reads %q, enabled %v", now.Label, now.Enabled)
	}
	for id, key := range map[string]rune{"tabs.text.smaller": 's', "tabs.text.larger": 'r'} {
		if row, _ := menuRow(rows, id); !row.Enabled || row.Hotkey != key {
			t.Errorf("%s at 90%%: enabled %v, key %q", id, row.Enabled, row.Hotkey)
		}
	}
	runRow(t, rows, "tabs.text.larger")
	runRow(t, rows, "tabs.text.larger")
	if s.pct != 110 {
		t.Fatalf("Larger twice from 90%%: %d%%", s.pct)
	}
	runRow(t, rows, "tabs.text.smaller")
	if s.pct != 100 {
		t.Fatalf("then Smaller: %d%%", s.pct)
	}
	for pct, id := range map[int]string{50: "tabs.text.smaller", 150: "tabs.text.larger"} {
		s.pct = pct
		if row, _ := menuRow(panelRows(s, "agent", &modelPanel{}, "", func() {}), id); row.Enabled {
			t.Errorf("at %d%%, %s is enabled", pct, id)
		}
	}
	s.pct, s.err = 90, errors.New("views are not attached")
	runRow(t, panelRows(s, "agent", &modelPanel{}, "", func() {}), "tabs.text.smaller")
	if len(s.kept) != 1 || !errors.Is(s.kept[0], s.err) || !strings.HasPrefix(s.kept[0].Error(), "Text size › Smaller for the agent panel: ") {
		t.Errorf("a refused step kept %v, not naming what failed", s.kept)
	}
}

// TestPanelRowsResumeTheirTab: Resume (u) sends the menu's own tab its SIGCONT, through the panel; with
// no tab (an empty panel's menu) it is disabled; a refusal is kept, named for what failed.
func TestPanelRowsResumeTheirTab(t *testing.T) {
	p := &modelPanel{}
	row, ok := menuRow(panelRows(struct{}{}, "agent", p, "t2", func() {}), "tabs.resume")
	if !ok || row.Label != "Resume" || row.Hotkey != 'u' || !row.Enabled {
		t.Fatalf("with a tab, the row is %q (%q), enabled %v", row.Label, row.Hotkey, row.Enabled)
	}
	row.Action.(widget.TerminalTabsMenuAction).Run("t2")
	if !slices.Equal(p.resumed, []widget.TerminalTabID{"t2"}) {
		t.Fatalf("Resume asked the panel for %v, want t2", p.resumed)
	}
	if row, _ := menuRow(panelRows(struct{}{}, "agent", p, "", func() {}), "tabs.resume"); row.Enabled {
		t.Error("an empty panel's Resume is enabled")
	}
	k := &maximizeHost{}
	failing := &resumeRefused{}
	r, _ := menuRow(panelRows(k, "terminal", failing, "t1", func() {}), "tabs.resume")
	r.Action.(widget.TerminalTabsMenuAction).Run("t1")
	if len(k.kept) != 1 || !strings.Contains(k.kept[0].Error(), "Resume a tab of the terminal panel") {
		t.Fatalf("a refused resume kept %v", k.kept)
	}
}

// resumeRefused is a panel whose Resume says no.
type resumeRefused struct{ modelPanel }

func (*resumeRefused) Resume(widget.TerminalTabID) error { return ErrResumeUnsupported }
