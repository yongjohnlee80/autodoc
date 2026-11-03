package tui

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// grantsWindow is Agent permissions' program: the grants are its preference, a revoke takes them
// back from it as revokeLua does, and questions are kept for the cell to answer.
type grantsWindow struct {
	grants  map[string]map[string]bool
	revoked [][]string // holder, then its capabilities, per RevokeGrants
	state   map[string]any
	opened  []string
	focused string
	asked   []Question
	notices []string
}

func (w *grantsWindow) Prefs() prefs                      { return prefs{luaGrants: w.grants} }
func (w *grantsWindow) Set(name string, v any)            { w.state[name] = v }
func (w *grantsWindow) Open(id string)                    { w.opened = append(w.opened, id) }
func (w *grantsWindow) Focus(id string)                   { w.focused = id }
func (w *grantsWindow) Ask(q Question) Asked              { w.asked = append(w.asked, q); return Asked{} }
func (w *grantsWindow) Notify(msg string)                 { w.notices = append(w.notices, msg) }
func (w *grantsWindow) Tr(id string) string               { return w.Trf(id) }
func (w *grantsWindow) Trf(id string, args ...any) string { return fill(englishText(id), args...) }
func (w *grantsWindow) RevokeGrants(holder string, caps []string) {
	w.revoked = append(w.revoked, append([]string{holder}, caps...))
	for _, c := range caps {
		delete(w.grants[holder], c)
	}
	if len(w.grants[holder]) == 0 {
		delete(w.grants, holder)
	}
}

func newGrantsCell(grants map[string]map[string]bool) (*grantsView, *grantsWindow) {
	w := &grantsWindow{grants: grants, state: map[string]any{}}
	return newGrantsView(w), w
}

// listed is the dialog's rows as who/capability pairs, in order.
func grantsListed(g *grantsView) []string {
	var out []string
	for _, r := range g.rows.All() {
		out = append(out, r.holder+" "+r.capability)
	}
	return out
}

// Agent permissions lists every capability granted, one row each: agents first, then plugins'
// packages, then gated scripts, each holder's by capability; each row names its holder, what kind
// of holder it is, and what the capability lets it do, as its card said it. An editor capability
// is described, not "a capability AutoDoc does not know".
func TestAgentPermissionsListsEveryGrantByHolder(t *testing.T) {
	g, w := newGrantsCell(map[string]map[string]bool{
		"plugin:globe":   {"workspace.read": true},
		"agent:zen":      {"terminal": true, "editor.read": true},
		"tools/run.lua":  {"clipboard": true},
		"agent:cloud":    {"editor.write": true},
		"agent:stranger": {"commands": false}, // a refusal is never kept, but a false reads as no grant
	})
	g.open()
	if want := []string{"agent:cloud editor.write", "agent:zen editor.read", "agent:zen terminal",
		"plugin:globe workspace.read", "tools/run.lua clipboard"}; !slices.Equal(grantsListed(g), want) {
		t.Fatalf("the rows %v, want %v", grantsListed(g), want)
	}
	if len(w.opened) != 1 || w.opened[0] != "grants" {
		t.Fatalf("the dialog opened %v, want grants", w.opened)
	}
	if w.focused != "grantTable" {
		t.Fatalf("the keyboard went to %q at open, want the list (grantTable)", w.focused)
	}
	cases := []struct {
		i               int
		who, kind, what string
	}{
		{0, "cloud", "agent", "insert, replace and delete text in your open documents"},
		{1, "zen", "agent", "read your open documents and buffers, and search in them"},
		{2, "zen", "agent", "run command lines in AutoDoc's terminal"},
		{3, "globe", "package", "read the files in your workspaces"},
		{4, "tools/run.lua", "Lua script", "read and write the clipboard"},
	}
	for _, c := range cases {
		row := g.rowOf(c.i, g.rows.All()[c.i])
		if row["who"] != c.who || row["kind"] != c.kind || !strings.HasPrefix(row["what"].(string), c.what) {
			t.Errorf("row %d: %v, want who %q, kind %q, what %q…", c.i, row, c.who, c.kind, c.what)
		}
		if strings.Contains(row["what"].(string), "does not know") {
			t.Errorf("row %d's capability is described as unknown: %v", c.i, row)
		}
	}
	if s := w.state["App.grantsStatus"]; s != "5 capability grant(s) to 4 holder(s). Revoke takes one back: its holder's next use of it asks you again." {
		t.Errorf("the status: %q", s)
	}
	if d := w.state["App.grantsDetail"]; d != "cloud (agent) holds editor.write: insert, replace and delete text in your open documents" {
		t.Errorf("the detail of the first row: %q", d)
	}

	empty, ew := newGrantsCell(nil)
	empty.open()
	if len(empty.rows.All()) != 0 || !strings.HasPrefix(ew.state["App.grantsStatus"].(string), "Nothing is granted.") {
		t.Errorf("no grants: rows %v, status %q", grantsListed(empty), ew.state["App.grantsStatus"])
	}
}

// Revoke asks first, naming the capability, its holder and what it lets it do; nothing is taken
// back until the user says Revoke. Then the row's capability alone is, and the user is told.
func TestRevokeTakesOneCapabilityBackOnlyOnceTheUserAgrees(t *testing.T) {
	g, w := newGrantsCell(map[string]map[string]bool{"agent:zen": {"terminal": true, "editor.read": true}})
	g.open()
	g.revoke(1) // agent:zen terminal
	if len(w.asked) != 1 || len(w.revoked) != 0 {
		t.Fatalf("after Revoke: asked %d, revoked %v; want one question and nothing taken back yet", len(w.asked), w.revoked)
	}
	q := w.asked[0]
	if q.Title != "revoke terminal from zen?" || !strings.Contains(q.Text, "zen holds terminal: run command lines") ||
		!strings.Contains(q.Text, "asks you again by a card") || q.Yes != "&Revoke" || q.No != "&Keep" {
		t.Fatalf("the question: %+v", q)
	}
	q.OnYes()
	if len(w.revoked) != 1 || !slices.Equal(w.revoked[0], []string{"agent:zen", "terminal"}) {
		t.Fatalf("revoked %v, want agent:zen terminal alone", w.revoked)
	}
	if len(w.notices) != 1 || w.notices[0] != "zen no longer holds terminal: its next use asks you again" {
		t.Fatalf("the notice: %v", w.notices)
	}
	g.PreferencesChanged() // the preference written: the list follows it
	if want := []string{"agent:zen editor.read"}; !slices.Equal(grantsListed(g), want) {
		t.Fatalf("after the revoke the rows %v, want %v", grantsListed(g), want)
	}
	g.revoke(5) // past the rows: nothing to ask
	if len(w.asked) != 1 {
		t.Fatalf("a revoke past the rows asked: %d", len(w.asked))
	}
}

// Revoke all takes back everything the row's holder holds, and nothing another holder does, once
// the user agrees.
func TestRevokeAllTakesEverythingItsHolderHolds(t *testing.T) {
	g, w := newGrantsCell(map[string]map[string]bool{
		"agent:zen":  {"terminal": true, "editor.read": true, "commands": true},
		"agent:juno": {"editor.read": true},
	})
	g.open()
	g.revokeAll(2) // a row of zen's: juno's is first
	if len(w.asked) != 1 || len(w.revoked) != 0 {
		t.Fatalf("after Revoke all: asked %d, revoked %v", len(w.asked), w.revoked)
	}
	if q := w.asked[0]; q.Title != "revoke everything zen holds?" || !strings.Contains(q.Text, "zen holds commands, editor.read, terminal.") {
		t.Fatalf("the question: %+v", q)
	}
	w.asked[0].OnYes()
	if len(w.revoked) != 1 || !slices.Equal(w.revoked[0], []string{"agent:zen", "commands", "editor.read", "terminal"}) {
		t.Fatalf("revoked %v, want all of zen's", w.revoked)
	}
	g.PreferencesChanged()
	if want := []string{"agent:juno editor.read"}; !slices.Equal(grantsListed(g), want) {
		t.Fatalf("after Revoke all the rows %v, want %v", grantsListed(g), want)
	}
}

// The list follows the preference while the dialog is open (a card answered meanwhile shows at
// once), the cursor staying on its row; closed, it doesn't read it.
func TestThePermissionsFollowThePreferenceKeepingTheCursor(t *testing.T) {
	g, w := newGrantsCell(map[string]map[string]bool{"agent:zen": {"terminal": true}, "plugin:globe": {"clipboard": true}})
	g.open()
	g.showDetail(1) // plugin:globe clipboard
	w.grants["agent:ann"] = map[string]bool{"editor.read": true}
	g.PreferencesChanged()
	if want := []string{"agent:ann editor.read", "agent:zen terminal", "plugin:globe clipboard"}; !slices.Equal(grantsListed(g), want) {
		t.Fatalf("after a grant the rows %v, want %v", grantsListed(g), want)
	}
	if w.state["App.grantsIndex"] != 2 || g.cursor != 2 {
		t.Fatalf("the cursor at %v (%d), want on plugin:globe clipboard, row 2", w.state["App.grantsIndex"], g.cursor)
	}
	g.closed()
	w.grants["agent:bo"] = map[string]bool{"commands": true}
	g.PreferencesChanged()
	if len(grantsListed(g)) != 3 {
		t.Fatalf("closed, the dialog read the preference: %v", grantsListed(g))
	}
}

// End to end: a capability an agent was granted, revoked from Agent permissions, is refused at the
// agent's next call and asked for again by its card, which names the agent as an agent; the
// preference no longer holds it, and Allow grants it again.
func TestARevokedCapabilityIsRefusedAndAskedForAgain(t *testing.T) {
	sockDir := t.TempDir()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: sockDir, LuaDir: t.TempDir()})
	r.connected(t)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool {
		r.h.agents.agentEnv(wsInfo{name: "kb"}, t.TempDir(), agentProfile{Codename: "zen"})
		return true
	})
	path := onLoop(r, r.h.editorSocketPath)
	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, path, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	token := onLoop(r, func() string { return r.h.agents.editorToken("zen") })
	read := func() *golibrpc.Error {
		_, err := cli.Call(ctx, "editor.read", token)
		var re *golibrpc.Error
		if errors.As(err, &re) {
			return re
		}
		if err != nil {
			t.Fatalf("editor.read: %v", err)
		}
		return nil
	}
	granted := func() bool { return onLoop(r, func() bool { return r.h.prefs.luaGrants["agent:zen"]["editor.read"] }) }

	if re := read(); re == nil || !strings.Contains(re.Message, "needs editor.read") {
		t.Fatalf("an ungranted read: %v", re)
	}
	r.s.WaitForText(t, "let the agent zen use editor.read?")
	r.s.WaitForText(t, "read your open documents")
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.closeDialog("confirm"); return true }) // as Allow does: the card closes
	r.s.WaitFor(t, "the read granted", func(string) bool { return granted() })
	if re := read(); re != nil {
		t.Fatalf("a granted read: %v", re)
	}

	onLoop(r, func() bool { r.h.runEntry("agents.permissions"); return true })
	r.s.WaitFor(t, "Agent permissions drawn", func(sc string) bool {
		return strings.Contains(sc, "Agent permissions") && strings.Contains(sc, "CAPABILITY") && strings.Contains(sc, "editor.read")
	})
	onLoop(r, func() bool {
		i := slices.IndexFunc(r.h.grantsView.rows.All(), func(g grantRow) bool { return g.holder == "agent:zen" && g.capability == "editor.read" })
		if i < 0 {
			t.Error("the dialog lists no editor.read of zen's")
		}
		r.h.grantsView.revoke(i)
		return true
	})
	r.s.WaitForText(t, "revoke editor.read from zen?")
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.closeDialog("confirm"); return true }) // as Allow does: the card closes
	r.s.WaitFor(t, "the read revoked", func(string) bool { return !granted() })
	r.s.WaitForText(t, "Nothing is granted.")

	if re := read(); re == nil || re.Code != codeEditorNotGranted || !strings.Contains(re.Message, "needs editor.read") {
		t.Fatalf("a read after the revoke: %v, want needs editor.read", re)
	}
	r.s.WaitForText(t, "let the agent zen use editor.read?")
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.closeDialog("confirm"); return true }) // as Allow does: the card closes
	r.s.WaitFor(t, "the read granted again", func(string) bool { return granted() })
	if re := read(); re != nil {
		t.Fatalf("a read granted again: %v", re)
	}
}

// The Go menu opens Agent permissions, and the dialog's buttons run its commands.
func TestTheGoMenuOpensAgentPermissions(t *testing.T) {
	main, err := os.ReadFile("qml/main.qml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), `MenuItem { text: qsTrId("autodoc.main.agent_permissions"); onTriggered: App.openGrants() }`) ||
		!strings.Contains(string(main), "Grants { id: grants }") {
		t.Fatal("main.qml has no Go › Agent permissions item opening the grants dialog")
	}
	dialog, err := os.ReadFile("qml/dialogs/Grants.qml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"App.grantRevoke(grantTable.currentIndex)", "App.grantRevokeAll(grantTable.currentIndex)", "App.grantsClosed()", "App.grantShown(index)"} {
		if !strings.Contains(string(dialog), want) {
			t.Errorf("Grants.qml doesn't run %s", want)
		}
	}
}

// From the keyboard, on screen: Agent permissions opens with the list holding the keys (Down moves
// its cursor, the detail following), and each button answers its letter with the list focused:
// r asks to revoke the row's capability, a to revoke everything its holder holds (Esc keeps
// both), and q closes the dialog.
func TestAgentPermissionsAnswersItsKeysFromTheList(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: t.TempDir()})
	r.connected(t)
	onLoop(r, func() bool {
		r.h.prefs.luaGrants = map[string]map[string]bool{"agent:zen": {"editor.read": true, "terminal": true}}
		r.h.runEntry("agents.permissions")
		return true
	})
	r.s.WaitFor(t, "Agent permissions drawn", func(sc string) bool {
		return strings.Contains(sc, "Agent permissions") && strings.Contains(sc, "zen (agent) holds editor.read")
	})
	r.s.WaitFor(t, "the list holding the keys", func(string) bool {
		return onLoop(r, func() bool { return core{r.h}.HasFocus("grantTable") })
	})

	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown})
	r.s.WaitForText(t, "zen (agent) holds terminal")

	r.keys(t, key('r'))
	r.s.WaitForText(t, "revoke terminal from zen?")
	r.keys(t, esc())
	r.s.WaitFor(t, "the revoke card gone", func(sc string) bool { return !strings.Contains(sc, "revoke terminal from zen?") })

	r.keys(t, key('a'))
	r.s.WaitForText(t, "revoke everything zen holds?")
	r.keys(t, esc())
	r.s.WaitFor(t, "the revoke-all card gone", func(sc string) bool { return !strings.Contains(sc, "revoke everything zen holds?") })
	if !onLoop(r, func() bool {
		return r.h.prefs.luaGrants["agent:zen"]["editor.read"] && r.h.prefs.luaGrants["agent:zen"]["terminal"]
	}) {
		t.Fatal("Esc on a revoke card took a grant back")
	}

	r.keys(t, key('q'))
	r.s.WaitFor(t, "the dialog closed", func(sc string) bool { return !strings.Contains(sc, "Agent permissions") })
}
