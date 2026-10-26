package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// editorClient is an agent's client of r's editor socket, as token: call answers a verb's result or
// its refusal, and fails the test on any other error.
func editorClient(t *testing.T, r *running, token string) func(verb string, params ...any) (any, *golibrpc.Error) {
	t.Helper()
	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, onLoop(r, r.h.editorSocketPath), msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })
	return func(verb string, params ...any) (any, *golibrpc.Error) {
		t.Helper()
		res, err := cli.Call(ctx, verb, append([]any{token}, params...)...)
		var re *golibrpc.Error
		if errors.As(err, &re) {
			return nil, re
		}
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		return res, nil
	}
}

// An agent opens a file by its path, and finds the commands first. editor.commands answers with no
// grant and asks the user nothing: :open is file.open_path's name, :e and :edit its aliases, and the
// editor's own :w (write) is among them.
// editor.open opens a path relative to the workspace's root and an absolute one, and editor.run's
// :e a name with a space in it, the whole line its path. A ':' name no command has is refused,
// naming editor.commands; an empty path is refused.
func TestAnAgentOpensAFileByItsPath(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "bee\n", "c d.md", "sea\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: t.TempDir()})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	call := editorClient(t, r, onLoop(r, func() string { return r.h.agents.editorToken("zen") }))

	res, re := call("editor.commands")
	if re != nil {
		t.Fatalf("editor.commands with no grant: %v", re)
	}
	rows, _ := res.([]any)
	i := slices.IndexFunc(rows, func(v any) bool { m, _ := v.(map[string]any); return m["id"] == "file.open_path" })
	if i < 0 {
		t.Fatalf("file.open_path is not among %d commands", len(rows))
	}
	open := rows[i].(map[string]any)
	if aliases, _ := open["aliases"].([]any); open["name"] != "open" || len(aliases) != 2 || aliases[0] != "e" || aliases[1] != "edit" || open["args"] != "<path>" {
		t.Fatalf("file.open_path's row: %v", open)
	}
	if !slices.ContainsFunc(rows, func(v any) bool {
		m, _ := v.(map[string]any)
		aliases, _ := m["aliases"].([]any)
		return m["name"] == "w" && m["source"] == "editor" && slices.Contains(aliases, any("write"))
	}) {
		t.Fatalf("the editor's own :w is not among the commands: %v", rows)
	}
	if onLoop(r, func() bool { return len(r.h.prefs.luaGrants["agent:zen"]) != 0 }) {
		t.Fatal("editor.commands asked for a grant")
	}

	onLoop(r, func() bool {
		r.h.prefs.luaGrants = map[string]map[string]bool{"agent:zen": {"commands": true}}
		return true
	})
	root := onLoop(r, func() string { return r.h.rootOf("kb") })
	if res, re := call("editor.open", "b.md"); re != nil || res.(map[string]any)["opening"] != filepath.Join(root, "b.md") {
		t.Fatalf("editor.open b.md: %v %v", res, re)
	}
	r.waitFile(t, "b.md")
	if _, re := call("editor.run", ":e c d.md"); re != nil {
		t.Fatalf(`editor.run ":e c d.md": %v`, re)
	}
	r.waitFile(t, "c d.md")
	if _, re := call("editor.open", filepath.Join(root, "a.md")); re != nil {
		t.Fatalf("editor.open by the absolute path: %v", re)
	}
	r.waitFile(t, "a.md")

	if _, re := call("editor.run", ":nope x"); re == nil || re.Code != codeEditorRefused ||
		!strings.Contains(re.Message, "no ':' command is named nope") || !strings.Contains(re.Message, "editor.commands") {
		t.Fatalf("an unknown ':' name: %v", re)
	}
	if _, re := call("editor.open", " "); re == nil || re.Code != codeEditorRefused || !strings.Contains(re.Message, "takes a path") {
		t.Fatalf("an empty path: %v", re)
	}
}

// A relative path is the editor's workspace's, whichever workspace the agent asking started in: an
// agent started in kb, the editor switched to notes, opens notes's file by its path there, and
// editor.document says it is foreign to the agent.
func TestAnAgentOpensARelativePathInTheEditorsWorkspace(t *testing.T) {
	agent, _ := recordingAgent(t, true)
	files := map[string][]string{"kb": {"a.md", "a\n"}, "notes": {"b.md", "b\n", "only.md", "notes only\n"}}
	_, r, _ := runAgentTUIIn(t, t.TempDir(), []agentProfile{{Name: "zen", Command: shellQuote(agent), Codename: "zen"}}, "zen", nil,
		Options{EditorDir: t.TempDir(), Workspace: "kb"}, files)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitForText(t, "ready")
	call := editorClient(t, r, onLoop(r, func() string { return r.h.agents.editorToken("zen") }))
	onLoop(r, func() bool {
		r.h.prefs.luaGrants = map[string]map[string]bool{"agent:zen": {"commands": true, "editor.read": true}}
		return true
	})

	r.switchTo(t, "notes")
	if _, re := call("editor.open", "only.md"); re != nil {
		t.Fatalf("editor.open only.md in notes: %v", re)
	}
	r.s.WaitFor(t, "notes's only.md open", func(string) bool {
		n := r.file()
		return n.open && n.ws == "notes" && n.path == "only.md"
	})
	res, re := call("editor.document")
	doc, _ := res.(map[string]any)
	if re != nil || doc["path"] != "only.md" || doc["foreign_workspace"] != true {
		t.Fatalf("editor.document after the open: %v %v", doc, re)
	}
}

// resolveOpenPath: absolute as it is, cleaned; ~/ under the home directory; relative to the root
// of the workspace in use, refused when none is listed; empty refused.
func TestOpenPathsResolve(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	if got, err := h.resolveOpenPath("/x/../y/z.md"); err != nil || got != "/y/z.md" {
		t.Errorf("an absolute path: %q %v", got, err)
	}
	if got, err := h.resolveOpenPath("~/notes/a b.md"); err != nil || got != filepath.Join(homeDir(), "notes", "a b.md") {
		t.Errorf("a ~/ path: %q %v", got, err)
	}
	if _, err := h.resolveOpenPath("notes/a.md"); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("a relative path with no workspace in use: %v", err)
	}
	if _, err := h.resolveOpenPath("  "); err == nil {
		t.Error("an empty path was taken")
	}
}

// editor.commands names the commands as they are now: a Lua command named open takes :open over, as a
// Lua command takes any name, so its row has :open and file.open_path's keeps :e and :edit.
func TestALuaCommandNamedOpenIsListedAsOwningIt(t *testing.T) {
	luaDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(luaDir, "open.lua"), []byte(`autodoc.command{ name = "open", run = function() end }`), 0o600); err != nil {
		t.Fatal(err)
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: t.TempDir(), LuaDir: luaDir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the Lua command registered", func(string) bool {
		return onLoop(r, func() bool {
			return r.h.cat != nil && r.h.cat.names["open"] != "" && r.h.cat.names["open"] != "file.open_path"
		})
	})
	call := editorClient(t, r, onLoop(r, func() string { return r.h.agents.editorToken("zen") }))
	res, re := call("editor.commands")
	if re != nil {
		t.Fatalf("editor.commands: %v", re)
	}
	rows, _ := res.([]any)
	row := func(match func(m map[string]any) bool) map[string]any {
		for _, v := range rows {
			if m, _ := v.(map[string]any); match(m) {
				return m
			}
		}
		return nil
	}
	lua := row(func(m map[string]any) bool { return m["source"] == "lua" && m["name"] == "open" })
	path := row(func(m map[string]any) bool { return m["id"] == "file.open_path" })
	if lua == nil || path == nil {
		t.Fatalf("the Lua open %v, file.open_path %v", lua, path)
	}
	if aliases, _ := path["aliases"].([]any); path["name"] != "e" || len(aliases) != 1 || aliases[0] != "edit" {
		t.Fatalf("file.open_path's row with :open taken over: %v", path)
	}
}
