package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/errs"
)

func TestPanelServicesRejectOtherFeaturesViews(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	for _, tc := range []struct {
		feature, foreign string
	}{
		{"agents", "editor"}, {"agents", "explorerTree"}, {"agents", "terminalView"},
		{"terminals", "agentView"}, {"terminals", "editor"},
	} {
		got := onLoop(r, func() []error {
			var c PanelHost = r.h.agents.c
			if tc.feature == "terminals" {
				c = r.h.terminals.c
			}
			callErr := c.CallView(tc.foreign, "forceActiveFocus")
			focusErr := c.Focus(tc.foreign)
			focused, queryErr := c.Focused(tc.foreign)
			if focused {
				queryErr = fmt.Errorf("foreign focus query disclosed a focused view")
			}
			return []error{callErr, focusErr, queryErr}
		})
		for i, err := range got {
			if !errors.Is(err, ErrPanelViewAccess) {
				t.Errorf("%s operation %d on %s: %v, want owned-view rejection", tc.feature, i, tc.foreign, err)
			}
		}
	}
	err := onLoop(r, func() error {
		return r.h.agents.c.ConfigureTerminal("terminalView", "not-run", "", "arg")
	})
	if !errors.Is(err, ErrPanelViewAccess) {
		t.Errorf("agent configured the shell feature's terminal: %v", err)
	}
}

func TestPanelNavigationAllowsOnlyBoundedKeyboardRestoration(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool {
		r.h.agents.c.OpenDrawer("explorer") // navigation to another panel is permitted
		return true
	})
	r.s.WaitFor(t, "explorer keyboard", func(string) bool { return r.focused("explorerTree") })
	err := onLoop(r, func() error { return r.h.agents.c.RestoreKeyboardPane("editor") })
	if err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "editor keyboard restored", func(string) bool { return r.focused("editor") })
	for _, id := range []string{"agentView", "confirm", "missingView"} {
		err := onLoop(r, func() error { return r.h.agents.c.RestoreKeyboardPane(id) })
		if !errors.Is(err, errs.ErrInvalidArgument) {
			t.Errorf("unbounded restoration to %s: %v", id, err)
		}
	}
}

func TestPanelScopesArePerContributorNotFeatureLabel(t *testing.T) {
	a, b := fixturePanel(), fixturePanel()
	a.ID, a.View = "first", "firstView"
	b.ID, b.View = "second", "secondView"
	r, err := newPanelRegistry([]Feature{
		&panelFixture{name: "sameLabel", specs: []PanelSpec{a}},
		&panelFixture{name: "sameLabel", specs: []PanelSpec{b}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.bind(core{}); err != nil {
		t.Fatal(err)
	}
	if r.hosts["first"] == r.hosts["second"] {
		t.Fatal("different contributors sharing a label share view permissions")
	}
	for panel, foreign := range map[string]string{"first": "secondView", "second": "firstView"} {
		if err := r.hosts[panel].CallView(foreign, "unused"); !errors.Is(err, ErrPanelViewAccess) {
			t.Errorf("contributor %s reached foreign view %s: %v", panel, foreign, err)
		}
	}
}

type panelBinderFixture struct {
	panelFixture
	bound PanelHost
	err   error
}

type panelOnlyServices struct{ PanelHost }

func TestAgentPanelBindingKeepsItsServicesOnAnInvalidHost(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	kept := onLoop(r, func() bool {
		before := r.h.agents.c
		err := r.h.agents.BindPanelHost(panelOnlyServices{r.h.terminals.c})
		return err != nil && r.h.agents.c == before
	})
	if !kept {
		t.Fatal("invalid agent service binding was accepted or replaced its valid scope")
	}
}

func (f *panelBinderFixture) BindPanelHost(c PanelHost) error { f.bound = c; return f.err }

func TestPanelScopeBindingUsesValidatedContributionsAndReportsRefusal(t *testing.T) {
	a, b := fixturePanel(), fixturePanel()
	b.ID, b.View = "other", "otherView"
	f := &panelBinderFixture{panelFixture: panelFixture{name: "owner", specs: []PanelSpec{a, b}}}
	r, err := newPanelRegistry([]Feature{f})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.bind(core{}); err != nil {
		t.Fatal(err)
	}
	if f.bound == nil || f.bound != r.hosts["custom"] || f.bound != r.hosts["other"] {
		t.Fatal("one contributor's panels received different service scopes")
	}
	err = f.bound.Focus("foreign")
	if !errors.Is(err, ErrPanelViewAccess) {
		t.Errorf("foreign access reached a tree lookup: %v", err)
	}
	f.err = errs.ErrUnsupported
	if err := r.bind(core{}); !errors.Is(err, errs.ErrUnsupported) {
		t.Errorf("binding refusal lost its error: %v", err)
	}
}

func TestPanelRegistryRejectsIdViewCrossCollisions(t *testing.T) {
	a, b := fixturePanel(), fixturePanel()
	b.ID, b.View = a.View, "otherView"
	if _, err := newPanelRegistry([]Feature{&panelFixture{name: "one", specs: []PanelSpec{a}}, &panelFixture{name: "two", specs: []PanelSpec{b}}}); err == nil || !strings.Contains(err.Error(), "component") {
		t.Errorf("foreign panel reused another feature's view as its id: %v", err)
	}
}

func TestOwnedPanelViewsRefuseCallsBeforeAttachment(t *testing.T) {
	f := &panelFixture{name: "owner", specs: []PanelSpec{fixturePanel()}}
	s := newScopedPanelHost(core{}, f, f.specs)
	queried, queryErr := s.Focused("customView")
	if queried {
		t.Fatal("a view was focused before its program existed")
	}
	for _, err := range []error{
		s.CallView("customView", "unused"), s.Focus("customView"), queryErr,
		s.ConfigureTerminal("customView", "not-run", ""), s.RestoreKeyboardPane("editor"),
	} {
		if !errors.Is(err, errs.ErrPrecondition) {
			t.Errorf("view operation before attachment returned %v", err)
		}
	}
}

func TestFailedPanelAssemblyKeepsThePublishedRegistry(t *testing.T) {
	h := newHost(NewSession("", nil), Options{})
	defer h.cancel()
	h.options(Options{})
	previous := h.reg
	before := h.prefs.panelDefs
	f := &panelBinderFixture{panelFixture: panelFixture{name: "refusing", specs: []PanelSpec{fixturePanel()}}, err: errs.ErrUnsupported}
	if err := h.assembleFeatures(f); !errors.Is(err, errs.ErrUnsupported) {
		t.Fatalf("binding refusal: %v", err)
	}
	if h.reg != previous || len(h.prefs.panelDefs) != len(before) || h.prefs.panelDefs[0].ID != before[0].ID {
		t.Fatal("a refused binding replaced the previously published registry/schema")
	}
	f.specs[0].ID = ""
	if err := h.assembleFeatures(f); err == nil {
		t.Fatal("an invalid schema was accepted")
	}
	if h.reg != previous || len(h.prefs.panelDefs) != len(before) {
		t.Fatal("an invalid schema changed the published registry/schema")
	}
}
