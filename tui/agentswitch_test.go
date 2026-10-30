//go:build linux || darwin

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// recordingAgent is an agent CLI that, its tty raw, writes every byte it is sent to report. With
// bracketed it asks for bracketed paste (mode 2004), as agent CLIs do.
func recordingAgent(t *testing.T, bracketed bool) (agent, report string) {
	t.Helper()
	dir := t.TempDir()
	agent, report = filepath.Join(dir, "recording-agent"), filepath.Join(dir, "report")
	mode := ""
	if bracketed {
		mode = `printf '\033[?2004h'` + "\n"
	}
	script := "#!/bin/sh\nstty raw -echo\n" + mode + "printf 'ready\\r\\n'\nexec cat > " + shellQuote(report) + "\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return agent, report
}

func reported(report string) string { b, _ := os.ReadFile(report); return string(b) }

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
	_, r, _ := runAgentTUIIn(t, dir, []agentProfile{{Name: "zen", Command: shellQuote(agent), Codename: "zen"}}, "zen", nil,
		Options{EditorDir: t.TempDir(), Workspace: "kb"}, files)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitForText(t, "ready")
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
	if sw["kind"] != switchEntered || fmt.Sprint(sw["from"]) != ref(kb) || fmt.Sprint(sw["to"]) != ref(notes) || sw["document"] != "b.md" ||
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
	if got := onLoop(r, func() wsRef { s, _ := r.h.agents.spawnWorkspace("zen"); return s }); got != kb {
		t.Errorf("the agent's workspace after the switch: %v, want kb's", got)
	}
	settings, err := os.ReadFile(filepath.Join(dir, "kb", ".claude", "settings.json"))
	if want := filepath.Join(dir, "kb", "bin", "autodoc") + "' --workspace-notice"; err != nil || !strings.Contains(string(settings), want) {
		t.Errorf("the folder's Claude settings: %v, want the hook %q:\n%s", err, want, settings)
	}
}

// switchTo enters ws and waits for it.
func (r *running) switchTo(t *testing.T, ws string) {
	t.Helper()
	r.h.p.Post(func() { r.h.enter(ws) })
	r.s.WaitFor(t, ws+" entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == ws }) })
}

// TestTheSwitchIsCoalesced (docs/decisions/crew.md#one-coalesced-switch-per-agent): an agent is
// told one switch from the workspace it last saw to the one in use, however many came between. kb →
// notes → other is one, kb → other; back to the one it saw is none; a rename (the same root) is
// renamed; a name the list doesn't hold yet waits; no workspace is left; a restart (a new token)
// starts again from its own.
func TestTheSwitchIsCoalesced(t *testing.T) {
	h := &Host{agents: &agents{instances: map[string]*agentInstance{
		"zen": {token: "t1", workspace: wsInfo{name: "kb", root: "/k"}},
	}}}
	h.workspaces = newRows(tuidecl.NewListModel("key", "label"), workspaceRowOf)
	list := func(ws ...wsInfo) { h.workspaces.Reset(ws) }
	list(wsInfo{name: "kb", root: "/k"}, wsInfo{name: "notes", root: "/n"}, wsInfo{name: "other", root: "/o"})
	take := func(ws string) (wsSwitch, bool) { h.ws = ws; return h.takeSwitch("zen") }

	if sw, ok := take("kb"); ok {
		t.Errorf("in its own workspace: %+v", sw)
	}
	h.ws = "notes" // a switch it isn't asked of
	if sw, ok := take("other"); !ok || sw != (wsSwitch{Kind: switchEntered, From: wsRef{"kb", "/k"}, To: wsRef{"other", "/o"}}) {
		t.Errorf("kb → notes → other: %+v %v, want one kb → other", sw, ok)
	}
	if sw, ok := take("other"); ok {
		t.Errorf("asked again: %+v", sw)
	}
	h.ws = "notes"
	if sw, ok := take("other"); ok {
		t.Errorf("other → notes → other: %+v", sw)
	}
	list(wsInfo{name: "kb", root: "/k"}, wsInfo{name: "renamed", root: "/o"})
	if sw, ok := take("renamed"); !ok || sw != (wsSwitch{Kind: switchRenamed, From: wsRef{"other", "/o"}, To: wsRef{"renamed", "/o"}}) {
		t.Errorf("other renamed: %+v %v", sw, ok)
	}
	if sw, ok := take("ghost"); ok {
		t.Errorf("a workspace the list doesn't hold yet: %+v", sw)
	}
	list(wsInfo{name: "kb", root: "/k"}, wsInfo{name: "ghost", root: "/g"})
	if sw, ok := take("ghost"); !ok || sw.From != (wsRef{"renamed", "/o"}) || sw.Kind != switchEntered {
		t.Errorf("once listed: %+v %v, want renamed → ghost", sw, ok)
	}
	if sw, ok := take(""); !ok || sw.Kind != switchLeft || sw.To != (wsRef{}) {
		t.Errorf("no workspace: %+v %v", sw, ok)
	}
	h.agents.instances["zen"].token = "t2" // restarted in its tab: it knows only kb
	if sw, ok := take("ghost"); !ok || sw.From != (wsRef{"kb", "/k"}) {
		t.Errorf("after a restart: %+v %v, want from kb", sw, ok)
	}
	if _, ok := h.takeSwitch("nobody"); ok {
		t.Error("an agent with no instance was told a switch")
	}

	spawn := wsRef{"kb", "/k"}
	for _, c := range []struct {
		sw   wsSwitch
		want []string
	}{
		{wsSwitch{Kind: switchEntered, From: spawn, To: wsRef{"notes", "/n"}, Document: "b.md"}, []string{"moved to workspace notes (/n), from kb (/k); open: b.md", "You stay in kb"}},
		{wsSwitch{Kind: switchRenamed, From: spawn, To: wsRef{"kb2", "/k"}}, []string{"kb was renamed kb2", "$AUTODOC_WORKSPACE still says kb"}},
		{wsSwitch{Kind: switchLeft, From: spawn}, []string{"no workspace open", "You stay in kb"}},
	} {
		n := c.sw.notice(spawn)
		for _, w := range c.want {
			if !strings.HasPrefix(n, "[AutoDoc] ") || strings.ContainsAny(n, "\r\n") || !strings.Contains(n, w) {
				t.Errorf("%s's notice %q lacks %q", c.sw.Kind, n, w)
			}
		}
	}
}

// TestAPeersRenameIsToldAsARename (§4.1): a peer's workspace.renamed of the workspace in use, through
// applyEvents, lists it under its new name at once, so an agent of it is told renamed, its root on
// both sides, before the relisting answers.
func TestAPeersRenameIsToldAsARename(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	sw := onLoop(r, func() wsSwitch {
		kb := r.h.wsRefOf("kb")
		r.h.agents.instances["zen"] = &agentInstance{token: "t", workspace: wsInfo{name: kb.Name, root: kb.Root}}
		r.h.applyEvents([]peerEvent{{seq: 1, kind: "workspace.renamed", workspace: "kb", client: "another", detail: "kb2"}})
		sw, _ := r.h.takeSwitch("zen")
		return sw
	})
	root := onLoop(r, func() string { return r.h.agents.instances["zen"].workspace.root })
	if root == "" || sw != (wsSwitch{Kind: switchRenamed, From: wsRef{"kb", root}, To: wsRef{"kb2", root}}) {
		t.Fatalf("a peer's rename told the agent %+v, want renamed kb → kb2 at %s", sw, root)
	}
}

// TestTheNoticeHookIsMergedIntoTheUsersSettings (§4.3): the hook is written into a new file; into one
// the user wrote it is added once, every key, hook, number and group of theirs kept (an empty one
// too), however often the agent starts; a file that isn't one JSON object (a second value or text
// after it included), or whose hooks aren't Claude's shape, is left as it is.
func TestTheNoticeHookIsMergedIntoTheUsersSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	cmd := noticeCommand(dir)
	ours := func(b []byte) int { return strings.Count(string(b), "--workspace-notice") }
	if err := mergeClaudeHook(path, cmd); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil || ours(b) != 1 {
		t.Fatalf("a new file: %v\n%s", err, b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("the file's mode %v, want 0600", fi.Mode().Perm())
	}

	user := `{"model": "opus", "cleanupPeriodDays": 12345678901234567, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "say done"}]}],` +
		` "UserPromptSubmit": [{"matcher": "kept-empty", "hooks": []}, {"matcher": "", "hooks": [{"type": "command", "command": "my-own"}, {"type": "command", "command": ` + mustJSON(cmd) + `}]}]}}`
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := mergeClaudeHook(path, cmd); err != nil {
			t.Fatal(err)
		}
	}
	b, _ = os.ReadFile(path)
	for _, keep := range []string{`"model": "opus"`, `12345678901234567`, `"say done"`, `"my-own"`, `"matcher": ""`, `"matcher": "kept-empty"`} {
		if !strings.Contains(string(b), keep) {
			t.Errorf("the user's %s was lost:\n%s", keep, b)
		}
	}
	if ours(b) != 1 {
		t.Errorf("the hook %d times, want once:\n%s", ours(b), b)
	}

	for _, bad := range []string{"not json", "[1, 2]", "null", `{"hooks": []}`, `{"hooks": {"UserPromptSubmit": {}}}`,
		`{"model": "opus"} {"theirs": true}`, `{"model": "opus"} and some notes`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := mergeClaudeHook(path, cmd); err == nil {
			t.Errorf("%q: merged, want left and said", bad)
		}
		if b, _ := os.ReadFile(path); string(b) != bad {
			t.Errorf("%q was rewritten as %q", bad, b)
		}
	}
}

func mustJSON(s string) string { b, _ := json.Marshal(s); return string(b) }
