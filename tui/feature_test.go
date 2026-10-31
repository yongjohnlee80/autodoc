package tui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/golib/parse/qml"
)

// fake is a feature that records what it hears.
type fake struct {
	name  string
	state map[string]any
	cmds  map[string]Command
	heard []string
	kinds []string
}

func (f *fake) Name() string                  { return f.name }
func (f *fake) State() map[string]any         { return f.state }
func (f *fake) Commands() map[string]Command  { return f.cmds }
func (f *fake) Connected()                    { f.heard = append(f.heard, "connected") }
func (f *fake) Entered(ws string)             { f.heard = append(f.heard, "entered "+ws) }
func (f *fake) Kinds() []string               { return f.kinds }
func (f *fake) Event(kind, ws, detail string) { f.heard = append(f.heard, kind+" "+ws+" "+detail) }

var noop = none(func() {})

func TestTheRegistryMergesFeatures(t *testing.T) {
	a := &fake{name: "a", state: map[string]any{"App.a": 1}, cmds: map[string]Command{"App.doA": noop}}
	b := &fake{name: "b", state: map[string]any{"App.b": "x"}, cmds: map[string]Command{"App.doB": noop}, kinds: []string{"b.changed"}}
	r, err := newRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.state["App.a"] != 1 || r.state["App.b"] != "x" || r.handlers["App.doA"] == nil || r.handlers["App.doB"] == nil {
		t.Errorf("merged %v %v", r.state, r.handlers)
	}
	r.connected()
	r.entered("kb")
	if !r.event("b.changed", "kb", "t1") || r.event("other", "", "") {
		t.Error("an event went to the wrong feature")
	}
	if got := strings.Join(b.heard, "|"); got != "connected|entered kb|b.changed kb t1" {
		t.Errorf("b heard %q", got)
	}
	var none *registry
	none.connected()
	none.entered("kb")
	if none.event("b.changed", "", "") {
		t.Error("an absent registry heard an event")
	}
}

func TestTheRegistryRefusesANameTwoFeaturesClaim(t *testing.T) {
	for name, b := range map[string]*fake{
		"a source":                    {name: "b", state: map[string]any{"App.a": 2}},
		"a command":                   {name: "b", cmds: map[string]Command{"App.doA": noop}},
		"a source named as a command": {name: "b", cmds: map[string]Command{"App.a": noop}},
	} {
		a := &fake{name: "a", state: map[string]any{"App.a": 1}, cmds: map[string]Command{"App.doA": noop}}
		if _, err := newRegistry(a, b); err == nil || !strings.Contains(err.Error(), "both a's and b's") {
			t.Errorf("%s claimed twice: %v", name, err)
		}
	}
}

// TestTheLegacyFeatureIsTheWholeHost: the host's own state and commands are one feature, so the
// program is what it was.
func TestTheLegacyFeatureIsTheWholeHost(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	n := onLoop(r, func() [2]int { return [2]int{len(r.h.reg.state), len(r.h.reg.handlers)} })
	want := onLoop(r, func() [2]int {
		var w [2]int
		for _, f := range r.h.reg.features {
			w[0] += len(f.State())
			w[1] += len(f.Commands())
		}
		return w
	})
	if n != want {
		t.Errorf("the registry has %v sources and commands, its features %v", n, want)
	}
	for k := range onLoop(r, r.h.state) {
		if _, ok := onLoop(r, func() map[string]any { return r.h.reg.state })[k]; !ok {
			t.Errorf("the host's %s is not in the registry", k)
		}
	}
}

// TestAFileTicketIsStaleWhenAnotherFileOpens: a file-scoped answer is stale once another file
// opens; a workspace-scoped one is not. With no sequence, nothing newer makes either stale.
func TestAFileTicketIsStaleWhenAnotherFileOpens(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	got := onLoop(r, func() [4]bool {
		file, ws := r.h.ticket(nil, TicketFile), r.h.ticket(nil, TicketWorkspace)
		r.h.ticket(nil, TicketFile) // no sequence: a later ticket supersedes nothing
		before := [2]bool{file.Current(), ws.Current()}
		r.h.file.gen++ // another file opened
		defer func() { r.h.file.gen-- }()
		return [4]bool{before[0], before[1], file.Current(), ws.Current()}
	})
	if got != [4]bool{true, true, false, true} {
		t.Errorf("current: file %v, workspace %v; after another file opened: file %v, workspace %v", got[0], got[1], got[2], got[3])
	}
}

// TestATicketIsStaleAfterANewerOneOrAWorkspaceSwitch: the latest answer wins, and an answer asked
// under another workspace never applies.
func TestATicketIsStaleAfterANewerOneOrAWorkspaceSwitch(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	got := onLoop(r, func() [4]bool {
		c := core{r.h}
		var seq uint64
		first := c.Ticket(&seq, TicketWorkspace)
		second := c.Ticket(&seq, TicketWorkspace)
		session := c.Ticket(new(uint64), TicketSession)
		ws := c.Ticket(new(uint64), TicketWorkspace)
		before := [2]bool{first.Current(), second.Current()}
		r.h.epoch++ // a workspace switch
		defer func() { r.h.epoch-- }()
		return [4]bool{before[0], before[1], session.Current(), ws.Current()}
	})
	if got != [4]bool{false, true, true, false} {
		t.Errorf("current: first %v, second %v, session-scoped after a switch %v, workspace-scoped after a switch %v", got[0], got[1], got[2], got[3])
	}
}

// TestCoreReachesTheHost: what a feature does through Core lands on the host.
func TestCoreReachesTheHost(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	c := core{r.h}
	if ws := onLoop(r, c.Workspace); ws != "kb" {
		t.Errorf("workspace %q", ws)
	}
	onLoop(r, func() bool { c.Notify("from a feature"); return true })
	r.waitNotice(t, "from a feature")
	out := make(chan any, 1)
	onLoop(r, func() bool {
		c.Go(func(ctx context.Context) any {
			v, err := c.Call(ctx, "workspace.list")
			if err != nil {
				return err
			}
			return v
		}, func(v any) { out <- v })
		return true
	})
	if v := <-out; v == nil {
		t.Error("no answer from the daemon through Core")
	} else if err, ok := v.(error); ok {
		t.Error(err)
	}
}

// coreKept is what the host keeps for Run, read on the loop.
func coreKept(r *running) []error {
	return onLoop(r, func() []error { r.h.mu.Lock(); defer r.h.mu.Unlock(); return slices.Clone(r.h.errs) })
}

// TestCoreOpensAFileByItsAbsolutePath: Core.OpenFile and Core.OpenAbsolute open the file at an
// absolute path: one a workspace indexes as that workspace's, one outside every workspace as in none.
func TestCoreOpensAFileByItsAbsolutePath(t *testing.T) {
	r, kb, _, abs := runOutside(t)
	c := core{r.h}
	r.h.p.Post(func() { c.OpenFile(filepath.Join(kb, "a.md")) })
	r.waitOpen(t, "kb", "a.md")
	r.h.p.Post(func() { c.OpenAbsolute(abs) })
	r.waitOpen(t, "", abs)
}

// TestCoreAsksWhenTheCardIsFree: Core.AskWhenFree never displaces the question open: it waits,
// the open one still asked and its callbacks not run, and is asked once that one is answered.
func TestCoreAsksWhenTheCardIsFree(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	type seen struct {
		shown   string
		waiting int
		got     string
	}
	// one loop turn: no answer from the real card comes between the steps
	got := onLoop(r, func() [3]seen {
		c, a := core{r.h}, r.h.asker
		var heard []string
		q := func(name string) Question {
			return Question{Title: name, OnYes: func() { heard = append(heard, name+":yes") }, OnCancel: func() { heard = append(heard, name+":cancel") }}
		}
		at := func() seen {
			s := seen{waiting: len(a.waiting), got: strings.Join(heard, " ")}
			if a.pending {
				s.shown = a.current.Title
			}
			return s
		}
		c.Ask(q("user"))
		c.AskWhenFree(q("free"))
		var out [3]seen
		out[0] = at()
		a.accepted()
		out[1] = at()
		a.accepted()
		out[2] = at()
		return out
	})
	want := [3]seen{{"user", 1, ""}, {"free", 0, "user:yes"}, {"", 0, "user:yes free:yes"}}
	if got != want {
		t.Fatalf("asked when free over an open question: %+v, want %+v", got, want)
	}
}

// TestCoreFocusesTheWidgetNamed: Core.Focus gives the keyboard to the widget id, from wherever it
// is; an id the layout has not is kept as an error, the keyboard left where it was.
func TestCoreFocusesTheWidgetNamed(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("explorer", "open")); return true })
	r.s.WaitFor(t, "the explorer open", func(string) bool { return onLoop(r, func() bool { return r.h.panelOpen["explorer"] }) })
	before := len(coreKept(r))
	got := onLoop(r, func() [6]bool {
		c := core{r.h}
		within := func(id string) bool { w, ok := r.h.p.Find(id); return ok && r.h.p.App().FocusWithin(w) }
		var out [6]bool
		c.Focus("editor")
		out[0], out[1] = within("editor"), within("explorerTree")
		c.Focus("explorerTree")
		out[2], out[3] = within("editor"), within("explorerTree")
		c.Focus("noSuchWidget")
		out[4], out[5] = within("editor"), within("explorerTree")
		return out
	})
	if want := [6]bool{true, false, false, true, false, true}; got != want {
		t.Fatalf("focus (editor, explorerTree) after Focus editor, Focus explorerTree, Focus of no widget: %v, want %v", got, want)
	}
	kept := coreKept(r)
	if len(kept) != before+1 || !strings.Contains(kept[len(kept)-1].Error(), `"noSuchWidget"`) {
		t.Fatalf("a focus of no widget kept %v (before: %d), want its error", kept, before)
	}
}

// TestCoreRingsTheTerminalsBell: in the terminal build Core.Bell rings the terminal's bell, once
// for each ring.
func TestCoreRingsTheTerminalsBell(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	before := r.s.Backend.Bells()
	onLoop(r, func() bool { core{r.h}.Bell(); return true })
	if n := r.s.Backend.Bells() - before; n != 1 {
		t.Fatalf("one Bell rang the terminal %d times", n)
	}
}

// TestCoreSetTextSaysALayoutWithoutTheTextArea: Core.SetText on an id that is no text area of the
// layout keeps that as an error, never passes over it; before the host has a layout at all, there
// is nothing to set and nothing is wrong.
func TestCoreSetTextSaysALayoutWithoutTheTextArea(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	before := len(coreKept(r))
	onLoop(r, func() bool { core{r.h}.SetText("noSuchArea", "text"); return true })
	kept := coreKept(r)
	if len(kept) != before+1 || !strings.Contains(kept[len(kept)-1].Error(), `no text area "noSuchArea"`) {
		t.Fatalf("SetText on no text area kept %v (before: %d), want its error", kept, before)
	}

	h := newHost(NewSession("unused", nil), Options{})
	t.Cleanup(h.cancel)
	core{h}.SetText(crewReplyText, "text") // no layout yet: must neither panic nor keep an error
	if len(h.errs) != 0 {
		t.Fatalf("SetText before a layout kept %v", h.errs)
	}
}

// TestCoreSaysTheStoreItsDaemonServes: Core.StoreID is the store_id the daemon said at hello: the
// identity of the store it serves.
func TestCoreSaysTheStoreItsDaemonServes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autodoc.db")
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{storeAt: path})
	r := attached(t, d)
	want, err := store.Identity(path)
	if err != nil || want == "" {
		t.Fatalf("the store's identity: %q, %v", want, err)
	}
	if got := onLoop(r, core{r.h}.StoreID); got != want {
		t.Fatalf("Core.StoreID %q, want the daemon's store %q", got, want)
	}
}

// pickFake is a feature with a picker of its own.
type pickFake struct {
	fake
	picks map[string]picker
}

func (f *pickFake) Picks() map[string]picker { return f.picks }

// A feature's picker is reached by its name through the App.pick* commands the registry serves.
func TestAFeaturesPickerIsReachedByName(t *testing.T) {
	p := newTestPick()
	r, err := newRegistry(&fake{name: "a"}, &pickFake{fake: fake{name: "b"}, picks: map[string]picker{"test": p}})
	if err != nil {
		t.Fatal(err)
	}
	str := func(s string) qml.SpecValue { return qml.SpecValue{Kind: qml.SpecValueString, Raw: s} }
	num := func(s string) qml.SpecValue { return qml.SpecValue{Kind: qml.SpecValueNumber, Raw: s} }
	if err := r.handlers["App.pickFilter"]([]qml.SpecValue{str("test"), str("notes")}); err != nil || len(p.shown()) != 2 {
		t.Fatalf("App.pickFilter(\"test\", \"notes\"): %v, rows %v", err, p.shown())
	}
	if err := r.handlers["App.pickChoose"]([]qml.SpecValue{str("test"), num("1")}); err != nil || !slices.Equal(p.chosen, []string{"notes/c.md"}) {
		t.Errorf("App.pickChoose(\"test\", 1): %v, chose %v", err, p.chosen)
	}
}

// Two features naming a picker alike are refused when the registry is built.
func TestTheRegistryRefusesAPickerTwoFeaturesName(t *testing.T) {
	a := &pickFake{fake: fake{name: "a"}, picks: map[string]picker{"test": newTestPick()}}
	b := &pickFake{fake: fake{name: "b"}, picks: map[string]picker{"test": newTestPick()}}
	if _, err := newRegistry(a, b); err == nil || err.Error() != "tui: the picker test is both a's and b's" {
		t.Errorf("a picker named twice: %v", err)
	}
	b.picks = map[string]picker{"other": newTestPick()}
	if _, err := newRegistry(a, b); err != nil {
		t.Errorf("two pickers named apart: %v", err)
	}
}
