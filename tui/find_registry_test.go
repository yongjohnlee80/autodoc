package tui

import (
	"slices"
	"strings"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// findableFake is a feature whose pane / searches: its rows, its cursor, and the rows find moved it
// to.
type findableFake struct {
	fake
	pane  string // the pane's name
	view  string
	rows  []string
	cur   int
	moved []int
}

func (f *findableFake) FindPane() FindPane {
	return FindPane{Name: f.pane, View: f.view, Rows: func() ([]string, int, func(int, int) error) {
		return slices.Clone(f.rows), f.cur, func(row, _ int) error {
			f.moved = append(f.moved, row)
			f.cur = row
			return nil
		}
	}}
}

// findablePanelFake is a panel feature finding in a view of its own, or not.
type findablePanelFake struct {
	findableFake
	spec PanelSpec
}

func (f *findablePanelFake) Panels() []PanelSpec { return []PanelSpec{f.spec} }

// A feature's pane is searched by /, n and N in the program without find.go naming it: the pane
// with the keyboard is found by its view, its rows read, its cursor moved by its own moveTo. The
// panes are searched in the features' order, the page first, and the hint names every one.
func TestAFeaturesPaneIsSearchedWithoutFindNamingIt(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	f := &findableFake{fake: fake{name: "zz", state: map[string]any{
		"App.zzRows": tuidecl.NewListModel("key", "label"), "App.zzOther": tuidecl.NewListModel("key", "label")}},
		pane: "birds", view: "zzList", rows: []string{"robin", "blue jay", "wren", "jaybird"}}
	h := newHost(NewSession(d.sock, nil), Options{})
	h.moreFeatures = []Feature{f}
	main, err := keyCatalog(h.features()).withGenerated(string(layout))
	if err != nil {
		t.Fatal(err)
	}
	// two lists beside the page: the birds', which / searches, and one no feature finds in
	anchor := "        Text { visible: App.diagnosticsShown; text: App.diagnosticsLine; color: Theme.syntax.alert }\n"
	if !strings.Contains(main, anchor) {
		t.Fatal("the layout has no diagnostics line to put the test's lists after")
	}
	main = strings.Replace(main, anchor, anchor+`        ListView {
            id: zzList
            model: App.zzRows
            textRole: "label"
        }
        ListView {
            id: zzOther
            model: App.zzOther
            textRole: "label"
        }
`, 1)
	s := decltest.RunWith(t, 200, 30, h.attach, // wide: the status line holds the whole hint
		h.options(Options{Layout: []byte(main)})...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	r.s.WaitForText(t, "connected — autodoc v-test")

	names := onLoop(r, func() []string {
		var out []string
		for _, p := range r.h.findPanes() {
			out = append(out, p.Name)
		}
		return out
	})
	if want := []string{"page", "explorer", "relations", "birds"}; !slices.Equal(names, want) {
		t.Fatalf("find searches %q, want %q: the page, then the features' order", names, want)
	}
	// the built-ins' hint is today's, word for word
	if got := findPaneList(onLoop(r, func() []FindPane { return r.h.findPanes()[:3] })); got != "the page, the explorer or the relations" {
		t.Errorf("the built-in panes are named %q", got)
	}
	// the explorer and the relations declare the views their bound panels have
	for id, name := range map[string]string{"explorer": "explorer", relationsPanelSpec().ID: "relations"} {
		view, bound := onLoop(r, func() string { p, _ := r.h.findPane(name); return p.View }), onLoop(r, func() string { return r.h.panelView(id) })
		if view == "" || view != bound {
			t.Errorf("the %s's find view is %q, its bound panel's %q", name, view, bound)
		}
	}

	// in a list no feature finds in, / names every pane, the feature's too
	if err := onLoop(r, func() error { return r.h.p.Call("zzOther", "forceActiveFocus") }); err != nil {
		t.Fatal(err)
	}
	r.keys(t, key('/'))
	r.s.WaitForText(t, "find: put the keyboard in the page, the explorer, the relations or the birds first")

	if err := onLoop(r, func() error { return r.h.p.Call("zzList", "forceActiveFocus") }); err != nil {
		t.Fatal(err)
	}
	r.keys(t, key('/'))
	r.s.WaitForText(t, "find in the birds — n next, N previous")
	r.keys(t, decltest.Type("JAY")...) // case-blind
	r.keys(t, enter())
	r.s.WaitForText(t, `find "JAY": 1 of 2 in the birds`)
	r.keys(t, key('n'))
	r.s.WaitForText(t, `find "JAY": 2 of 2 in the birds`)
	r.keys(t, key('n')) // wraps
	r.s.WaitForText(t, `find "JAY": 1 of 2 in the birds`)
	r.keys(t, key('N')) // back, wrapping
	r.s.WaitForText(t, `find "JAY": 2 of 2 in the birds`)
	moved := onLoop(r, func() []int { return slices.Clone(f.moved) })
	if want := []int{1, 3, 1, 3}; !slices.Equal(moved, want) {
		t.Fatalf("find moved the birds' cursor to %v, want %v", moved, want)
	}
	if onLoop(r, func() string { return r.h.find.target }) != "birds" {
		t.Error("the find is not the birds'")
	}
}

// A pane find could not route to is the registry's error: one without a name, a view or rows, a
// name or a view another feature's pane has, and a panel feature's that is none of its panels'.
func TestTheRegistryRefusesAPaneFindCannotRoute(t *testing.T) {
	pane := func(name, pane, view string) *findableFake {
		return &findableFake{fake: fake{name: name}, pane: pane, view: view}
	}
	noRows := &findableFakeNoRows{fake: fake{name: "b"}}
	for _, c := range []struct {
		name string
		fs   []Feature
		want string
	}{
		{"no name", []Feature{pane("a", "", "aList")}, "a's find pane needs a name, a view and its rows"},
		{"no view", []Feature{pane("a", "birds", "")}, "a's find pane needs a name, a view and its rows"},
		{"no rows", []Feature{noRows}, "b's find pane needs a name, a view and its rows"},
		{"a name twice", []Feature{pane("a", "birds", "aList"), pane("b", "birds", "bList")}, "the find pane birds is both a's and b's"},
		{"a view twice", []Feature{pane("a", "birds", "aList"), pane("b", "fish", "aList")}, "the find view aList is both a's and b's"},
		{"none of its panels' views", []Feature{&findablePanelFake{findableFake: *pane("a", "birds", "aList"),
			spec: PanelSpec{ID: "aPanel", View: "aPanelView", Edge: func(prefs) string { return "left" }}}}, "a finds in aList, which is none of its panels' views"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := newRegistry(c.fs...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("the registry said %v, want %q", err, c.want)
			}
		})
	}
	// a panel feature finding in its own panel's view, and two distinct panes, are taken in order
	r, err := newRegistry(pane("a", "birds", "aList"), &findablePanelFake{findableFake: *pane("b", "fish", "bView"),
		spec: PanelSpec{ID: "bPanel", View: "bView", Edge: func(prefs) string { return "left" }}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range r.finds {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"birds", "fish"}) {
		t.Fatalf("the registry's panes are %q, want birds then fish", names)
	}
}

// findableFakeNoRows declares a pane with no rows.
type findableFakeNoRows struct{ fake }

func (*findableFakeNoRows) FindPane() FindPane { return FindPane{Name: "fish", View: "bList"} }

// Search › Find in page with the keyboard in the explorer: the page's hit gives the page the keyboard
// (the page's FindPane moves it there).
func TestAPageHitFromAnotherPaneGivesThePageTheKeyboard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one kestrel\ntwo\n", "b.md", "b\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.leader(t, 'e')
	r.s.WaitForText(t, "explorer")
	if got := onLoop(r, func() string { return r.h.findTarget() }); got != "explorer" {
		t.Fatalf("the keyboard is in %q before Find in page, want the explorer", got)
	}
	onLoop(r, func() bool { r.h.openFindInPage(); return true })
	r.s.WaitForText(t, "find in the page — n next, N previous")
	r.keys(t, decltest.Ctrl('u'))
	r.keys(t, decltest.Type("kestrel")...)
	r.keys(t, enter())
	r.s.WaitForText(t, `find "kestrel": 1 of 1 in the page`)
	if got := onLoop(r, func() string { return r.h.findTarget() }); got != findPage {
		t.Fatalf("after the page's hit the keyboard is in %q, want the page", got)
	}
}
