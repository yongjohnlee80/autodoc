package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// TestAgentsReachThePageThroughTheEditorSocket: an agent's call is refused until the user allows
// its capability, then works; an edit is one undo step, and one at a revision the text has left
// is refused; editor.run needs commands; editor.add_command writes lua/<name>.lua only on yes, and
// never over a file there. The socket is 0600, and the agents are told of it.
func TestAgentsReachThePageThroughTheEditorSocket(t *testing.T) {
	sockDir, luaDir := t.TempDir(), t.TempDir()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: sockDir, LuaDir: luaDir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")

	path := onLoop(r, r.h.editorSocketPath)
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 || filepath.Dir(path) != sockDir {
		t.Fatalf("the socket %s: %v %v", path, fi, err)
	}
	env := strings.Join(onLoop(r, func() []string { return r.h.agents.agentEnv(wsInfo{name: "kb"}, t.TempDir(), "zen") }), "\n")
	if !strings.Contains(env, "AUTODOC_EDITOR="+path) || !strings.Contains(env, "AUTODOC_AGENT=zen") {
		t.Fatalf("the agent's environment: %s", env)
	}

	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, path, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	call := func(verb string, params ...any) (map[string]any, *golibrpc.Error) {
		t.Helper()
		res, err := cli.Call(ctx, verb, append([]any{"zen"}, params...)...)
		var re *golibrpc.Error
		if errors.As(err, &re) {
			return nil, re
		}
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		m, _ := res.(map[string]any)
		return m, nil
	}

	if _, re := call("editor.read"); re == nil || re.Code != codeEditorNotGranted || !strings.Contains(re.Message, "needs editor.read") {
		t.Fatalf("an ungranted read: %v", re)
	}
	r.s.WaitForText(t, "let the Lua script agent:zen use editor.read?")
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	r.s.WaitFor(t, "the read granted", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.luaGrants["agent:zen"]["editor.read"] })
	})
	got, re := call("editor.read")
	if re != nil || got["text"] != "kestrel\n" {
		t.Fatalf("editor.read: %v %v", got, re)
	}
	rev := got["revision"]

	onLoop(r, func() bool {
		r.h.prefs.luaGrants["agent:zen"]["editor.write"] = true
		return true
	})
	if _, re := call("editor.insert", 1, 1, "# ", rev); re != nil {
		t.Fatalf("editor.insert: %v", re)
	}
	if text := r.editorText(); text != "# kestrel\n" {
		t.Fatalf("after the insert: %q", text)
	}
	if _, re := call("editor.replace", 1, 3, 1, 10, "hawk", rev); re == nil || re.Code != codeEditorStale {
		t.Fatalf("an edit at the revision before: %v", re)
	}
	r.undo()
	r.wantText(t, "one undo", "kestrel\n")

	if _, re := call("editor.run", "table 1 1"); re == nil || re.Code != codeEditorNotGranted {
		t.Fatalf("an ungranted run: %v", re)
	}

	// add_command: the call waits for the user; Yes writes the file and reloads
	type answer struct {
		res map[string]any
		re  *golibrpc.Error
	}
	ask := func(name, src string) chan answer {
		ch := make(chan answer, 1)
		go func() {
			res, err := cli.Call(ctx, "editor.add_command", "zen", name, src)
			var re *golibrpc.Error
			errors.As(err, &re)
			m, _ := res.(map[string]any)
			ch <- answer{m, re}
		}()
		return ch
	}
	if _, re := call("editor.add_command", "bad", "this is not lua"); re == nil || !strings.Contains(re.Message, "does not compile") {
		t.Fatalf("a source that does not compile: %v", re)
	}
	ch := ask("hi", `autodoc.command{ name = "hi-agent", run = function(ed) ed:insert("from the agent") end }`)
	r.s.WaitForText(t, "let the agent zen add the Lua command file hi.lua?")
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	select {
	case a := <-ch:
		if a.re != nil || a.res["installed"] != true {
			t.Fatalf("add_command: %v %v", a.res, a.re)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("add_command never answered")
	}
	if fi, err := os.Stat(filepath.Join(luaDir, "hi.lua")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("lua/hi.lua: %v %v", fi, err)
	}
	r.page("", 1, 1)
	r.mustRun(t, "hi-agent")
	r.wantText(t, ":hi-agent", "from the agent")

	if _, re := call("editor.add_command", "hi", `autodoc.notify("again")`); re == nil || !strings.Contains(re.Message, "exists") {
		t.Fatalf("a second hi.lua: %v", re)
	}
	ch = ask("nope", `autodoc.notify("never")`)
	r.s.WaitForText(t, "let the agent zen add the Lua command file nope.lua?")
	onLoop(r, func() bool { r.h.asker.rejected(); return true })
	if a := <-ch; a.re == nil || a.re.Code != codeEditorRefused {
		t.Fatalf("a refused add_command: %v %v", a.res, a.re)
	}
	if _, err := os.Stat(filepath.Join(luaDir, "nope.lua")); err == nil {
		t.Fatal("a refused command was written")
	}
}
