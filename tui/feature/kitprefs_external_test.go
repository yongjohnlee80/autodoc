package feature_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// store is a host's preferences as a package other than the kit's keeps them: it implements the
// kit's writer contract with exported methods alone, so the host's preferences can implement it
// once the kit is a package of its own.
type store struct{ m map[string]string }

func (s *store) Value(name string) string                         { return s.m[name] }
func (s *store) PanelGeometry(id, edge string) (size, length int) { return 30, 85 }
func (s *store) SetValue(name, v string) {
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[name] = v
}

var _ feature.PrefsWriter = (*store)(nil)

// A row bound from outside the kit's package (BoundPref, PrefBinding) reads, keeps and resets its
// value in the host's own store through the row machinery a host uses (ReadRows, RowState), so its
// binding's wiring is what is exercised; a feature's row (ValuePref) is read by name through the
// same view.
func TestARowIsBoundByAHostInAnotherPackage(t *testing.T) {
	s := &store{}
	row := feature.BoundPref("host.switch", []string{"off", "on"}, feature.PrefBinding{
		Read:  func(p feature.PrefsWriter, v string) { p.SetValue("host.switch", v) },
		Value: func(p feature.PrefsView) string { return p.Value("host.switch") },
		Reset: func(p feature.PrefsWriter) { p.SetValue("host.switch", "off") },
	})
	row.Index = "App.hostSwitchIndex" // the chooser's row is read through the row's Value binding
	own := feature.ValuePref("feature.mode", "a", []string{"a", "b"})
	rows := []feature.PrefSpec{row, own}

	feature.ReadRows(s, map[string]any{"host.switch": "on", "feature.mode": "b"}, rows)
	if s.m["host.switch"] != "on" || s.m["feature.mode"] != "b" {
		t.Fatalf("read through the rows: %v, want host.switch on and feature.mode b", s.m)
	}
	if st := feature.RowState(s, rows); st["App.hostSwitchIndex"] != 1 {
		t.Errorf("the chooser's row through the binding's Value: %v, want 1 (on)", st)
	}
	feature.ReadRows(s, map[string]any{"host.switch": "maybe", "feature.mode": "z"}, rows) // values the rows refuse
	if s.m["host.switch"] != "off" || s.m["feature.mode"] != "a" {
		t.Errorf("refused values: %v, want each row's default through its Reset (off, a)", s.m)
	}
	var view feature.PrefsView = s
	if size, length := view.PanelGeometry("x", "left"); size != 30 || length != 85 {
		t.Errorf("PanelGeometry through the view: %d %d", size, length)
	}
}
