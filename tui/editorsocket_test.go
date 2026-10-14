package tui

import (
	"context"
	"errors"
	"fmt"
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
	if !strings.Contains(env, "AUTODOC_EDITOR="+path) || !strings.Contains(env, "AUTODOC_AGENT=zen") ||
		!strings.Contains(env, "AUTODOC_EDITOR_TOKEN=") {
		t.Fatalf("the agent's environment: %s", env)
	}

	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, path, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	token := onLoop(r, func() string { return r.h.agents.editorToken("zen") })
	callAs := func(tok, verb string, params ...any) (map[string]any, *golibrpc.Error) {
		t.Helper()
		res, err := cli.Call(ctx, verb, append([]any{tok}, params...)...)
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
	call := func(verb string, params ...any) (map[string]any, *golibrpc.Error) {
		t.Helper()
		return callAs(token, verb, params...)
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

	// another agent, granted write, is known by its own token: zen naming it gets nothing of its
	onLoop(r, func() bool {
		r.h.agents.editorToken("other")
		r.h.prefs.luaGrants["agent:other"] = map[string]bool{"editor.write": true, "commands": true}
		r.h.prefs.luaGrants["agent:zen"]["editor.write"] = false
		return true
	})
	if _, re := callAs("other", "editor.insert", 1, 1, "X"); re == nil || re.Code != codeEditorNotGranted || !strings.Contains(re.Message, "not an agent's token") {
		t.Fatalf("a codename for a token: %v", re)
	}
	if _, re := call("editor.insert", 1, 1, "X"); re == nil || !strings.Contains(re.Message, "needs editor.write") {
		t.Fatalf("zen after its write was taken away: %v", re)
	}
	onLoop(r, func() bool { r.h.prefs.luaGrants["agent:zen"]["editor.write"] = true; return true })

	// a request that stopped waiting before the loop reached it does nothing when the loop does
	r.h.editorSock.wait.Store(int64(200 * time.Millisecond))
	release := make(chan struct{})
	r.h.p.Post(func() { <-release })
	if _, err := cli.Call(ctx, "editor.insert", token, 1, 1, "LATE"); err == nil {
		t.Fatal("an edit the loop did not reach in time answered")
	}
	close(release)
	r.h.editorSock.wait.Store(int64(editorRequestWait))
	if text := r.editorText(); strings.Contains(text, "LATE") {
		t.Fatalf("the expired edit ran: %q", text)
	}

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
			res, err := cli.Call(ctx, "editor.add_command", token, name, src)
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
	// the whole source is shown: what the card says is what is written; a longer one is refused
	long := "-- a long command\n" + strings.Repeat("local x = 1\n", 60)
	if _, re := call("editor.add_command", "long", long); re == nil || !strings.Contains(re.Message, "at most 60 lines") {
		t.Fatalf("a source longer than the card: %v", re)
	}
	deep := "local a = 1\n" + strings.Repeat("local b = 2\n", 30) + `autodoc.notify("the deep line")`
	ch = ask("deep", deep)
	r.s.WaitForText(t, "let the agent zen add the Lua command file deep.lua?")
	if txt := askText(r); txt != deep {
		t.Fatalf("the card shows %q, not the whole source", txt)
	}
	// a file created while the card is open is never replaced
	if err := os.WriteFile(filepath.Join(luaDir, "deep.lua"), []byte("-- the user's"), 0o600); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	if a := <-ch; a.re == nil || a.re.Code != codeEditorRefused {
		t.Fatalf("Yes over a file made meanwhile: %v %v", a.res, a.re)
	}
	if b, _ := os.ReadFile(filepath.Join(luaDir, "deep.lua")); string(b) != "-- the user's" {
		t.Fatalf("deep.lua replaced: %q", b)
	}
	// two cards for one name: the first is replaced (refused), the second's Yes writes
	first := ask("twin", `autodoc.notify("one")`)
	r.s.WaitForText(t, "let the agent zen add the Lua command file twin.lua?")
	second := ask("twin", `autodoc.notify("two")`)
	if a := <-first; a.re == nil || a.re.Code != codeEditorRefused {
		t.Fatalf("the replaced card: %v %v", a.res, a.re)
	}
	r.s.WaitFor(t, "the second card", func(string) bool {
		return strings.Contains(askText(r), `"two"`)
	})
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	if a := <-second; a.re != nil {
		t.Fatalf("the second card's Yes: %v", a.re)
	}
	if b, _ := os.ReadFile(filepath.Join(luaDir, "twin.lua")); string(b) != `autodoc.notify("two")` {
		t.Fatalf("twin.lua is %q", b)
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

// askText is the question card's text as the screen has it.
func askText(r *running) string {
	return onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.askText"); return fmt.Sprint(v.Raw) })
}

// TestTheTokenRegistryIsTheLoops: agents starting (their tokens made on the UI loop) while other
// agents call the socket: every lookup is the loop's too, so -race sees no shared access.
func TestTheTokenRegistryIsTheLoops(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: t.TempDir()})
	r.s.WaitForText(t, "connected — autodoc v-test")
	path := onLoop(r, r.h.editorSocketPath)
	token := onLoop(r, func() string { return r.h.agents.editorToken("zen") })
	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, path, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 100 {
			r.h.p.Post(func() { r.h.agents.editorToken(fmt.Sprintf("agent%d", i)) })
		}
	}()
	for range 100 {
		if _, err := cli.Call(ctx, "editor.read", token); err == nil {
			t.Fatal("an ungranted read answered") // zen has no grant: each call is refused, after the lookup
		}
	}
	<-done
}
