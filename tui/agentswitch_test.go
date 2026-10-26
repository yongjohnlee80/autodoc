//go:build linux || darwin

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// TestAnAgentIsToldOfAWorkspaceSwitch (ADR 1792988759): an agent started in kb stays in kb when
// the user switches the editor to notes. Its folder and guide are kb's, and nothing is made for
// notes. editor.workspace answers spawn kb, current notes, and the one switch, with both roots, and
// nothing after its cursor. editor.document and editor.buffers mark the notes buffer
// foreign_workspace, and the kb one not. A parameter can't choose the spawn.
func TestAnAgentIsToldOfAWorkspaceSwitch(t *testing.T) {
	agent, report := fakeAgent(t)
	dir := t.TempDir()
	files := map[string][]string{"kb": {"a.md", "a\n"}, "notes": {"b.md", "b\n"}}
	_, r, _ := runAgentTUIIn(t, dir, []agentProfile{{Name: "zen", Command: shellQuote(agent), Codename: "zen"}}, "zen", nil,
		Options{EditorDir: t.TempDir(), Workspace: "kb"}, files)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent started", func(string) bool { _, err := os.Stat(report); return err == nil })
	onLoop(r, func() bool {
		r.h.prefs.luaGrants = map[string]map[string]bool{"agent:zen": {"editor.read": true}}
		return true
	})
	// opened as an agent's OpenRef does: the agent holds the keyboard, so SPC is its
	r.h.p.Post(func() { r.h.openIn("kb", "a.md") })
	r.s.WaitFor(t, "a.md open", func(string) bool { n := r.file(); return n.open && n.path == "a.md" })

	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, onLoop(r, r.h.editorSocketPath), msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	token := onLoop(r, func() string { return r.h.agents.editorToken("zen") })
	call := func(verb string, params ...any) (any, error) {
		t.Helper()
		return cli.Call(ctx, verb, append([]any{token}, params...)...)
	}
	must := func(verb string, params ...any) map[string]any {
		t.Helper()
		res, err := call(verb, params...)
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		m, _ := res.(map[string]any)
		return m
	}
	kb := onLoop(r, func() wsRef { return r.h.wsRefOf("kb") })
	notes := onLoop(r, func() wsRef { return r.h.wsRefOf("notes") })
	if kb.Root == "" || notes.Root == "" || kb.Root == notes.Root {
		t.Fatalf("the workspaces' roots: %v %v", kb, notes)
	}
	ref := func(w wsRef) string { return fmt.Sprint(w.value()) }

	before := must("editor.workspace")
	if fmt.Sprint(before["spawn"]) != ref(kb) || fmt.Sprint(before["current"]) != ref(kb) || len(before["switches"].([]any)) != 0 {
		t.Fatalf("before the switch: %v", before)
	}
	if doc := must("editor.document"); doc["spawn_workspace"] != "kb" || doc["foreign_workspace"] != nil {
		t.Errorf("kb's document asked by a kb agent: %v", doc)
	}

	r.h.p.Post(func() { r.h.enter("notes") })
	r.s.WaitFor(t, "notes entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "notes" }) })
	r.h.p.Post(func() { r.h.openIn("notes", "b.md") })
	r.s.WaitFor(t, "b.md open", func(string) bool { n := r.file(); return n.open && n.path == "b.md" })

	after := must("editor.workspace")
	sw, _ := after["switches"].([]any)
	if fmt.Sprint(after["spawn"]) != ref(kb) || fmt.Sprint(after["current"]) != ref(notes) || len(sw) != 1 {
		t.Fatalf("after the switch: %v", after)
	}
	one, _ := sw[0].(map[string]any)
	if one["kind"] != switchEntered || fmt.Sprint(one["from"]) != ref(kb) || fmt.Sprint(one["to"]) != ref(notes) || one["at"] == "" {
		t.Errorf("the switch: %v", one)
	}
	if next := must("editor.workspace", after["cursor"]); len(next["switches"].([]any)) != 0 {
		t.Errorf("after the cursor: %v", next)
	}
	doc := must("editor.document")
	if doc["path"] != "b.md" || doc["workspace"] != "notes" || doc["spawn_workspace"] != "kb" || doc["foreign_workspace"] != true {
		t.Errorf("notes' document asked by a kb agent: %v", doc)
	}
	res, err := call("editor.buffers")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range res.([]any) {
		b := row.(map[string]any)
		if foreign := b["foreign_workspace"] == true; foreign != (b["workspace"] == "notes") {
			t.Errorf("a buffer of %v marked foreign %v: %v", b["workspace"], foreign, b)
		}
	}
	var re *golibrpc.Error
	if _, err := call("editor.workspace", "notes"); !errors.As(err, &re) {
		t.Errorf("editor.workspace given a workspace for a cursor: %v, want refused", err)
	}

	guide, err := os.ReadFile(filepath.Join(dir, "kb", "CLAUDE.md"))
	if err != nil || !strings.Contains(string(guide), "name: `kb`") {
		t.Errorf("the agent's guide after the switch: %v\n%s", err, guide)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the switch made a folder for notes: %v", err)
	}
	if got := onLoop(r, func() wsRef { s, _ := r.h.agents.spawnWorkspace("zen"); return s }); got != kb {
		t.Errorf("the agent's workspace after the switch: %v, want kb's", got)
	}
}

// The switch record keeps the last keptSwitches: a cursor older than the oldest kept reads from it,
// told lost; one at the newest reads nothing. A rename is one record, its root on both sides.
func TestTheSwitchRecordIsARing(t *testing.T) {
	h := &Host{}
	for i := range keptSwitches + 8 {
		h.noteSwitch(switchEntered, wsRef{Name: fmt.Sprint("w", i), Root: "/r"}, wsRef{Name: fmt.Sprint("w", i+1), Root: "/r"})
	}
	got, lost := h.switchesAfter(0)
	if len(got) != keptSwitches || !lost || got[0].Seq != 9 {
		t.Fatalf("from the start: %d kept from %v, lost %v", len(got), got[0].Seq, lost)
	}
	if got, lost := h.switchesAfter(h.switchSeq); len(got) != 0 || lost {
		t.Errorf("from the newest: %d, lost %v", len(got), lost)
	}
	if got, lost := h.switchesAfter(h.switchSeq - 1); len(got) != 1 || lost {
		t.Errorf("from the one before the newest: %d, lost %v", len(got), lost)
	}
	h.noteSwitch(switchEntered, wsRef{Name: "same", Root: "/s"}, wsRef{Name: "same", Root: "/s"})
	if n := h.switchSeq; n != keptSwitches+8 {
		t.Errorf("entering the workspace in use was recorded: seq %d", n)
	}
	// renamed: the list already names the new one, as after this TUI's own rename (settings.go)
	h.workspaces = newRows(tuidecl.NewListModel("key", "label"), workspaceRowOf)
	h.workspaces.Reset([]wsInfo{{name: "new", root: "/n"}})
	h.noteRename("old", "new")
	last := h.switches[len(h.switches)-1]
	if last.Kind != switchRenamed || last.From != (wsRef{Name: "old", Root: "/n"}) || last.To != (wsRef{Name: "new", Root: "/n"}) {
		t.Errorf("the rename: %+v", last)
	}
}
