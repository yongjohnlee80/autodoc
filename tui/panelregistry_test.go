package tui

import (
	"reflect"
	"strings"
	"testing"
)

type panelFixture struct {
	name       string
	specs      []PanelSpec
	stateCalls int
}

func (f *panelFixture) Name() string { return f.name }
func (f *panelFixture) State() map[string]any {
	f.stateCalls++
	return map[string]any{"App." + f.name + "Value": f.name}
}
func (*panelFixture) Commands() map[string]Command { return nil }
func (f *panelFixture) Panels() []PanelSpec        { return f.specs }

func fixturePanel() PanelSpec {
	return PanelSpec{ID: "custom", View: "customView", Edge: func(prefs) string { return "left" },
		Places: []string{"left"}, Size: [2]int{10, 90}, Length: [2]int{10, 100},
		Default: func(string) (int, int) { return 30, 70 }}
}

func TestPanelRegistriesSnapshotEachProgramAndBindItsHooks(t *testing.T) {
	left, right := 0, 0
	a, b := fixturePanel(), fixturePanel()
	a.Open = func(PanelHost) { left++ }
	b.Open = func(PanelHost) { right++ }
	f := &panelFixture{name: "first", specs: []PanelSpec{a}}
	g := &panelFixture{name: "second", specs: []PanelSpec{b}}
	r1, err := newRegistry(f)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := newRegistry(g)
	if err != nil {
		t.Fatal(err)
	}
	if f.stateCalls != 1 || g.stateCalls != 1 {
		t.Fatalf("State must be read once per feature: %d, %d", f.stateCalls, g.stateCalls)
	}
	f.specs[0].ID = "changed"
	f.specs[0].Places[0] = "right"
	if r1.panels.specs[0].ID != "custom" || r1.panels.specs[0].Places[0] != "left" {
		t.Fatal("mutating the contributor changed the program's snapshot")
	}
	r1.panels.specs[0].Open(nil)
	if left != 1 || right != 0 {
		t.Fatalf("first program invoked another program's hook: %d, %d", left, right)
	}
	r2.panels.specs[0].Open(nil)
	if left != 1 || right != 1 {
		t.Fatalf("program hooks share runtime state: %d, %d", left, right)
	}
	if _, ok := r1.state["App.secondValue"]; ok {
		t.Fatal("second program's source leaked into the first")
	}
}

func TestPanelRegistryRejectsAmbiguousOrInvalidContributions(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*PanelSpec)
		want string
	}{
		{"id", func(s *PanelSpec) { s.ID = "" }, "without an id"},
		{"view", func(s *PanelSpec) { s.View = "" }, "without an id"},
		{"edge", func(s *PanelSpec) { s.Edge = nil }, "without an id"},
		{"default", func(s *PanelSpec) { s.Default = nil }, "invalid geometry"},
		{"size bounds", func(s *PanelSpec) { s.Size = [2]int{90, 10} }, "invalid geometry"},
		{"length bounds", func(s *PanelSpec) { s.Length = [2]int{10, 101} }, "invalid geometry"},
		{"empty place", func(s *PanelSpec) { s.Places = []string{""} }, "empty or repeated"},
		{"repeated place", func(s *PanelSpec) { s.Places = []string{"left", "left"} }, "empty or repeated"},
		{"default bounds", func(s *PanelSpec) { s.Default = func(string) (int, int) { return 1, 70 } }, "outside its bounds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixturePanel()
			tc.edit(&s)
			if _, err := newRegistry(&panelFixture{name: "bad", specs: []PanelSpec{s}}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid contribution: %v, want %q", err, tc.want)
			}
		})
	}
	a, b := fixturePanel(), fixturePanel()
	if _, err := newRegistry(&panelFixture{name: "one", specs: []PanelSpec{a}}, &panelFixture{name: "two", specs: []PanelSpec{b}}); err == nil || !strings.Contains(err.Error(), "panel custom is both") {
		t.Fatalf("duplicate panel was accepted: %v", err)
	}
	b.ID = "other"
	if _, err := newRegistry(&panelFixture{name: "one", specs: []PanelSpec{a}}, &panelFixture{name: "two", specs: []PanelSpec{b}}); err == nil || !strings.Contains(err.Error(), "panel view") {
		t.Fatalf("shared panel view was accepted: %v", err)
	}
}

func TestFeaturePanelSchemaControlsPreferenceReadback(t *testing.T) {
	f := &panelFixture{name: "custom", specs: []PanelSpec{fixturePanel()}}
	r, err := newRegistry(f)
	if err != nil {
		t.Fatal(err)
	}
	p := prefsForPanels(map[string]any{
		"tui.custom.left.size": "45", "tui.custom.left.length": "60",
		"tui.custom.float": "10,20,30,40",
	}, false, r.panels.specs)
	if size, length := p.panelGeometry("custom", "left"); size != 45 || length != 60 {
		t.Fatalf("contributed panel's preferences were ignored: %d × %d", size, length)
	}
	if size, length := p.panelGeometry("missing", "left"); size != 0 || length != 0 {
		t.Fatalf("unknown panel got geometry: %d × %d", size, length)
	}
	st := floatState(p.panelFloat, r.panels.specs)
	if st["App.customFloating"] != true || st["App.customX"] != float64(10) {
		t.Fatalf("contributed panel's float was ignored: %v", st)
	}
	if _, exists := st["App.terminalFloating"]; exists {
		t.Fatal("a built-in absent from this program leaked into its float state")
	}
}

func TestDynamicPanelGeometryRejectsInvalidWrites(t *testing.T) {
	r, err := newRegistry(&panelFixture{name: "custom", specs: []PanelSpec{fixturePanel()}})
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{reg: r, prefs: prefs{panelGeo: map[string]int{"existing": 42}}}
	before := map[string]int{"existing": 42}
	h.keepPanelGeo("missing", "size", 30)
	h.keepPanelGeo("custom", "unknown", 30)
	h.keepPanelGeo("custom", "size", 1)
	h.keepPanelGeo("custom", "length", 101)
	h.panelResized("custom", 1, 50)
	h.panelResized("custom", 40, 101)
	h.openDrawer("missing")
	h.closeDrawer("missing")
	h.panelOpened("missing")
	h.panelClosed("missing")
	if !reflect.DeepEqual(h.prefs.panelGeo, before) || len(h.prefWrites) != 0 || len(h.panelOpen) != 0 {
		t.Fatalf("invalid geometry changed program state: %+v", h.prefs.panelGeo)
	}
}

func TestTerminalAndAgentFeaturesAreProgramOwned(t *testing.T) {
	a := newHost(NewSession("", nil), Options{})
	b := newHost(NewSession("", nil), Options{})
	defer a.cancel()
	defer b.cancel()
	a.options(Options{})
	b.options(Options{})
	if a.reg.panels == b.reg.panels || a.agents.agentRows == b.agents.agentRows {
		t.Fatal("programs share a panel registry or agent model")
	}
	a.terminals.started = true
	a.terminals.before = "relationsList"
	a.agents.agentRunning = "first"
	if b.terminals.started || b.terminals.before != "" || b.agents.agentRunning != "" {
		t.Fatal("programs share terminal or agent runtime state")
	}
	for _, name := range []string{"App.terminalShown", "App.agentShown"} {
		if _, exists := a.state()[name]; exists {
			t.Errorf("legacy still owns feature source %s", name)
		}
		if _, exists := a.reg.state[name]; !exists {
			t.Errorf("feature source %s missing from program", name)
		}
	}
	for _, name := range []string{"App.toggleTerminal", "App.toggleAgent"} {
		if _, exists := a.commands()[name]; exists {
			t.Errorf("legacy still owns feature command %s", name)
		}
		if _, exists := a.reg.handlers[name]; !exists {
			t.Errorf("feature command %s missing from program", name)
		}
	}
}

func TestConfigureTerminalRefusesMissingAndOtherViews(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	for _, tc := range []struct {
		id, want string
	}{{"missingView", "no terminal view"}, {"editor", "not a configurable terminal"}} {
		err := onLoop(r, func() error { return (core{r.h}).ConfigureTerminal(tc.id, "not-run", "", "arg") })
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("configure %s: %v, want %q", tc.id, err, tc.want)
		}
	}
}

func TestEmptyProgramPanelSchemaStaysEmpty(t *testing.T) {
	r, err := newRegistry()
	if err != nil {
		t.Fatal(err)
	}
	p := prefsForPanels(map[string]any{"tui.terminal.bottom.size": "50"}, false, r.panels.specs)
	if len(p.geometrySpecs()) != 0 || len(p.panelGeo) != 0 {
		t.Fatal("a program with no contributed panels inherited the standalone built-ins")
	}
	if st := floatState(p.panelFloat, p.geometrySpecs()); len(st) != 0 {
		t.Fatalf("empty program invented float sources: %v", st)
	}
}

func TestPanelPriorityTiesKeepFeatureContributionOrder(t *testing.T) {
	a, b, c := fixturePanel(), fixturePanel(), fixturePanel()
	a.ID, a.View, a.Priority = "third", "thirdView", 20
	b.ID, b.View, b.Priority = "first", "firstView", 10
	c.ID, c.View, c.Priority = "second", "secondView", 10
	r, err := newPanelRegistry([]Feature{
		&panelFixture{name: "earlier", specs: []PanelSpec{a, b}},
		&panelFixture{name: "later", specs: []PanelSpec{c}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{r.specs[0].ID, r.specs[1].ID, r.specs[2].ID}
	if !reflect.DeepEqual(got, []string{"first", "second", "third"}) {
		t.Fatalf("priority/tie order = %v", got)
	}
}
