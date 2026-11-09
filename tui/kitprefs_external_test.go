package tui_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/tui"
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

var _ tui.PrefsWriter = (*store)(nil)

// A row bound from outside the kit's package (BoundPref, PrefBinding) reads, keeps and resets its
// value in the host's own store; a feature's row (ValuePref) is named, and its view is read-only.
func TestARowIsBoundByAHostInAnotherPackage(t *testing.T) {
	s := &store{}
	b := tui.PrefBinding{
		Valid: func(v string) bool { return v == "on" || v == "off" },
		Read:  func(p tui.PrefsWriter, v string) { p.SetValue("host.switch", v) },
		Value: func(p tui.PrefsView) string { return p.Value("host.switch") },
		Reset: func(p tui.PrefsWriter) { p.SetValue("host.switch", "off") },
	}
	row := tui.BoundPref("host.switch", nil, b)
	if row.Name != "host.switch" {
		t.Fatalf("the row's name: %q", row.Name)
	}
	b.Reset(s)
	b.Read(s, "on")
	var view tui.PrefsView = s
	if got := b.Value(view); got != "on" || !b.Valid("off") || b.Valid("maybe") {
		t.Errorf("the host's binding: value %q, valid off %v, valid maybe %v", got, b.Valid("off"), b.Valid("maybe"))
	}
	if size, length := view.PanelGeometry("x", "left"); size != 30 || length != 85 {
		t.Errorf("PanelGeometry through the view: %d %d", size, length)
	}
	if feature := tui.ValuePref("feature.mode", "a", []string{"a", "b"}); feature.Name != "feature.mode" {
		t.Errorf("a feature's row: %q", feature.Name)
	}
}
