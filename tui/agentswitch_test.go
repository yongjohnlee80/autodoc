//go:build linux || darwin

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// TestAnAgentIsToldOfAWorkspaceSwitch
// (docs/decisions/crew.md#workspace-switches-are-told-not-imposed): an agent started in kb stays in
// kb when the user switches the editor to notes, and nothing is typed into its terminal.
// editor.workspace, with no grant, answers spawn_workspace kb, workspace_in_use notes and one
// switch, with both roots, the open document and its notice, then none: entering notes again is no
// switch either. editor.document and editor.buffers mark the notes buffer foreign_workspace, and
// the kb one not. A parameter is refused. The folder's .claude/settings.json runs the notice hook.
func TestAnAgentIsToldOfAWorkspaceSwitch(t *testing.T) {
	agent, report := recordingAgent(t, true)
	dir := t.TempDir()
	files := map[string][]string{"kb": {"a.md", "a\n"}, "notes": {"b.md", "b\n"}}
	_, r, _ := runAgentTUIIn(t, dir, []agents.AgentProfile{{Name: "zen", Command: agents.ShellQuote(agent), Codename: "zen"}}, "zen", nil,
		Options{EditorDir: t.TempDir(), Workspace: "kb"}, files)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	onLoop(r, func() bool { r.h.agents.ToggleAgent(); return true })
	r.s.WaitForText(t, "ready")
	r.h.p.Post(func() { r.h.openIn("kb", "a.md") })
	r.s.WaitFor(t, "a.md open", func(string) bool { n := r.file(); return n.open && n.path == "a.md" })

	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, onLoop(r, r.h.editorSocketPath), msgpackrpc.New(nil), golibrpc.ClientNetwork(ipc.Network), golibrpc.WithConnDialer(ipc.RPCDialer))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	token := onLoop(r, func() string { return r.h.agents.EditorToken("zen") })
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
	kb := onLoop(r, func() agents.WsRef { return r.h.wsRefOf("kb") })
	notes := onLoop(r, func() agents.WsRef { return r.h.wsRefOf("notes") })
	if kb.Root == "" || notes.Root == "" || kb.Root == notes.Root {
		t.Fatalf("the workspaces' roots: %v %v", kb, notes)
	}
	ref := func(w agents.WsRef) string { return fmt.Sprint(w.Value()) }

	// no grant is held: editor.workspace answers all the same, and asks the user nothing
	before := must("editor.workspace")
	if fmt.Sprint(before["spawn_workspace"]) != ref(kb) || fmt.Sprint(before["workspace_in_use"]) != ref(kb) || before["switch"] != nil {
		t.Fatalf("before the switch: %v", before)
	}
	if onLoop(r, func() bool { return len(r.h.prefs.luaGrants["agent:zen"]) != 0 }) || strings.Contains(r.s.String(), "editor.read") {
		t.Fatalf("editor.workspace asked for a grant:\n%s", r.s.String())
	}
	onLoop(r, func() bool {
		r.h.prefs.luaGrants = map[string]map[string]bool{"agent:zen": {"editor.read": true}}
		return true
	})
	if doc := must("editor.document"); doc["spawn_workspace"] != "kb" || doc["foreign_workspace"] != nil {
		t.Errorf("kb's document asked by a kb agent: %v", doc)
	}

	r.switchTo(t, "notes")
	r.h.p.Post(func() { r.h.openIn("notes", "b.md") })
	r.s.WaitFor(t, "b.md open", func(string) bool { n := r.file(); return n.open && n.path == "b.md" })

	after := must("editor.workspace")
	sw, _ := after["switch"].(map[string]any)
	if fmt.Sprint(after["spawn_workspace"]) != ref(kb) || fmt.Sprint(after["workspace_in_use"]) != ref(notes) || sw == nil {
		t.Fatalf("after the switch: %v", after)
	}
	notice, _ := sw["notice"].(string)
	if sw["kind"] != agents.SwitchEntered || fmt.Sprint(sw["from"]) != ref(kb) || fmt.Sprint(sw["to"]) != ref(notes) || sw["document"] != "b.md" ||
		!strings.HasPrefix(notice, "[AutoDoc] ") || !strings.Contains(notice, notes.Root) || !strings.Contains(notice, "You stay in kb") {
		t.Errorf("the switch: %v", sw)
	}
	if again := must("editor.workspace"); again["switch"] != nil {
		t.Errorf("asked again: %v", again)
	}
	onLoop(r, func() bool { r.h.enter("notes"); return true }) // a reconnect enters the workspace in use again
	if again := must("editor.workspace"); again["switch"] != nil {
		t.Errorf("after notes entered again: %v", again)
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
	if _, err := call("editor.workspace", int64(0)); !errors.As(err, &re) {
		t.Errorf("editor.workspace given a param: %v, want refused", err)
	}

	time.Sleep(200 * time.Millisecond) // anything typed on the switch would have reached it by now
	if got := reported(report); got != "" {
		t.Errorf("the switch typed into the agent's terminal: %q", got)
	}
	guide, err := os.ReadFile(filepath.Join(dir, "kb", "CLAUDE.md"))
	if err != nil || !strings.Contains(string(guide), "name: `kb`") {
		t.Errorf("the agent's guide after the switch: %v\n%s", err, guide)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the switch made a folder for notes: %v", err)
	}
	if got := onLoop(r, func() agents.WsRef { s, _ := r.h.agents.SpawnWorkspace("zen"); return s }); got != kb {
		t.Errorf("the agent's workspace after the switch: %v, want kb's", got)
	}
	// the notice runs the build by its path, never the folder's wrapper or a bare autodoc
	bin := onLoop(r, func() string { return r.h.agents.AgentBin() })
	hooks := claudeHookCommands(t, filepath.Join(dir, "kb"))
	if want := agents.ShellQuote(bin) + " --workspace-notice"; !slices.Contains(hooks["UserPromptSubmit"], want) {
		t.Errorf("the folder's Claude hooks: %q, want UserPromptSubmit's %q", hooks, want)
	}
}

// TestAPeersRenameIsToldAsARename (docs/decisions/crew.md#one-coalesced-switch-per-agent): a peer's
// workspace.renamed of the workspace in use, through applyEvents, lists it under its new name at
// once, its root kept, before the relisting answers: so the workspace an agent is handed next is
// the renamed one at the same root, which it is told as a rename (TestTheSwitchIsCoalesced).
func TestAPeersRenameIsToldAsARename(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	refs := onLoop(r, func() [3]agents.WsRef {
		kb := r.h.wsRefOf("kb")
		r.h.applyEvents([]peerEvent{{seq: 1, kind: "workspace.renamed", workspace: "kb", client: "another", detail: "kb2"}})
		return [3]agents.WsRef{kb, r.h.wsRefOf("kb2"), r.h.wsRefOf("kb")}
	})
	before, after, gone := refs[0], refs[1], refs[2]
	if before.Root == "" || after != (agents.WsRef{"kb2", before.Root}) || gone.Root != "" {
		t.Fatalf("a peer's rename: kb was %+v; kb2 is %+v and kb %+v, want kb2 at kb's root and kb no longer listed", before, after, gone)
	}
}

// TestTheWorkspaceInUseIsNamedWithItsRoot: the host hands the agents the workspace in use as it
// lists it (wsRefOf): its name and root; the name alone for one it doesn't list yet; none for no
// workspace. The agents tell a switch from it (TestTheSwitchIsCoalesced).
func TestTheWorkspaceInUseIsNamedWithItsRoot(t *testing.T) {
	h := &Host{}
	h.workspaces = feature.NewRows(tuidecl.NewListModel("key", "label"), workspaceRowOf)
	h.workspaces.Reset([]wsInfo{{name: "kb", root: "/k"}, {name: "notes", root: "/n"}})
	for name, want := range map[string]agents.WsRef{"kb": {"kb", "/k"}, "notes": {"notes", "/n"}, "ghost": {Name: "ghost"}, "": {}} {
		if got := h.wsRefOf(name); got != want {
			t.Errorf("wsRefOf(%q) = %+v, want %+v", name, got, want)
		}
	}
}
