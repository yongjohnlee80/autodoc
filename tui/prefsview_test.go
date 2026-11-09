package tui

import (
	"slices"
	"testing"
)

// A feature's view of the preferences is a snapshot: one taken before a change keeps the old
// value, and a fresh one has the new, for a host's row and a feature's alike. The defaults read
// by name, and the panels' geometry, are the host's own.
func TestAPreferencesViewIsASnapshot(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	flip := map[string]string{"true": "false", "false": "true"}
	for _, name := range []string{prefWrap, prefPluginPixels} { // a host's row, and a feature's
		got := onLoop(r, func() [3]string {
			old := core{r.h}.Prefs()
			was := old.Value(name)
			core{r.h}.SetPref(name, flip[was])
			return [3]string{was, old.Value(name), core{r.h}.Prefs().Value(name)}
		})
		if got[1] != got[0] || got[2] != flip[got[0]] {
			t.Errorf("%s: before %q, the old view after SetPref %q (want it unchanged), a fresh view %q (want %q)", name, got[0], got[1], got[2], flip[got[0]])
		}
	}

	p := defaultPrefs()
	for name, want := range map[string]string{prefTermEdge: "bottom", prefExplorer: "left", prefWrap: "true", prefTermTheme: "page"} {
		if v := p.Value(name); v != want {
			t.Errorf("the default of %s read by name: %q, want %q", name, v, want)
		}
	}
	if v := p.Value("no.such.preference"); v != "" {
		t.Errorf("a name no row declares: %q, want \"\"", v)
	}
	var view PrefsView = p
	s1, l1 := view.PanelGeometry("terminal", "bottom")
	s2, l2 := p.panelGeometry("terminal", "bottom")
	if s1 != s2 || l1 != l2 {
		t.Errorf("PanelGeometry through the view: %d×%d, the host's %d×%d", s1, l1, s2, l2)
	}
}

// A row's change runs in order: the new value is in place when its State and Changed read it, and
// the store's write comes after them.
func TestARowsChangeRunsInOrder(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	var steps []string
	row := ValuePref("test.feature.order", "a", nil)
	row.State = func(p PrefsView) map[string]any {
		steps = append(steps, "state "+p.Value("test.feature.order"))
		return nil
	}
	row.Changed = func(p PrefsView) {
		queued := len(r.h.prefWrites) > 0 && slices.ContainsFunc(r.h.prefWrites, func(w [2]string) bool { return w[0] == "test.feature.order" })
		steps = append(steps, "changed "+p.Value("test.feature.order")+map[bool]string{true: " stored", false: " unstored"}[queued])
	}
	got := onLoop(r, func() []string {
		r.h.prefRows = append(r.h.prefRows, row)
		core{r.h}.SetPref("test.feature.order", "b")
		queued := slices.ContainsFunc(r.h.prefWrites, func(w [2]string) bool { return w[0] == "test.feature.order" && w[1] == "b" })
		return append(steps, map[bool]string{true: "stored b", false: "not stored"}[queued])
	})
	// State may run again as the program shows its rows; what counts is the order of the first
	// of each, and that no step saw the old value
	first := func(step string) int { return slices.Index(got, step) }
	if first("state b") < 0 || first("changed b unstored") < first("state b") || got[len(got)-1] != "stored b" ||
		slices.ContainsFunc(got, func(s string) bool { return s == "state a" || s == "changed a unstored" }) {
		t.Errorf("the steps of a change: %v, want the State with b, then Changed with b before the store's write, then the write", got)
	}
}

// foreignView is preferences that are not the host's: a feature's row reads through it as through any
// view, by name.
type foreignView map[string]string

func (f foreignView) Value(name string) string                        { return f[name] }
func (f foreignView) PanelGeometry(string, string) (size, length int) { return 30, 85 }

// A feature's own row (ValuePref) answers its State through any view, not only the host's: none
// narrows it back to the host's preferences.
func TestAFeaturesRowReadsThroughAnyView(t *testing.T) {
	r := runTUI(t, NewSession("unused", nil), Options{GUI: guiBuild()})
	rows := onLoop(r, func() []PrefSpec { return slices.Clone(r.h.prefRows) })
	n := 0
	for _, s := range rows {
		if !s.byName || s.State == nil {
			continue
		}
		n++
		func() {
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("the row %s's State panicked on a view that is not the host's: %v", s.Name, v)
				}
			}()
			s.State(foreignView{s.Name: "x"})
		}()
	}
	if n == 0 {
		t.Fatal("no feature row with a State: the cell reads nothing")
	}
}
