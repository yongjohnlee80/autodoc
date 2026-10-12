package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// PLUGIN CALLS (ADR 1791762474 §4.5, §5.7, §5.8) — protocol 3's host.call, through the running
// program: the test plugin is this binary (plugins_test.go), its answers read from its log.

// markLua is the Lua the calls run: lua.mark writes its line into the page, lua.boom fails.
const markLua = `
autodoc.command{ name = "mark", run = function(ed, args, line) ed:insert("<" .. line .. ">") end }
autodoc.command{ name = "boom", run = function(ed) error("it broke") end }
`

// callTUI runs the TUI with the plugins in dir and markLua.
func callTUI(t *testing.T, dir string) (*running, string) {
	t.Helper()
	lua := t.TempDir()
	if err := os.WriteFile(filepath.Join(lua, "mark.lua"), []byte(markLua), 0o600); err != nil {
		t.Fatal(err)
	}
	return pluginLuaTUI(t, dir, nil, lua)
}

// results are the plugin.results the raw plugin has been sent, as it wrote them down.
func results(t *testing.T, logs, name string) []string {
	return slices.DeleteFunc(said(t, logs, name), func(s string) bool { return !strings.HasPrefix(s, "result ") })
}

// TestAPluginCallsTheCatalog (§5.7, §5.8): a declared call runs, a Lua entry on the attached
// editor, and answers ok; the entry's error is failed; an undeclared id, and a plugin's, refused;
// an id declared that no entry has, unknown. Each from inside the plugin's own Command and Key
// handlers, which wait for the answer without a deadlock; each answered once.
func TestAPluginCallsTheCatalog(t *testing.T) {
	dir := t.TempDir()
	installPlugin3(t, dir, "caller", "dialog", "caller", `calls = ["lua.mark", "lua.boom", "lua.nope"]
[dialog]
width = 60
height = 10
[[commands]]
id = "mark"
title = "Mark"
[[commands]]
id = "boom"
title = "Boom"
[[commands]]
id = "undeclared"
title = "Undeclared"
[[commands]]
id = "unknown"
title = "Unknown"
[[commands]]
id = "plugin"
title = "Plugin"
`)
	r, logs := callTUI(t, dir)
	for _, id := range []string{"mark", "boom", "undeclared", "unknown", "plugin"} {
		onLoop(r, func() bool { r.h.plugins.runPluginCommand("caller", id); return true })
		waitSaid(t, logs, "caller", "called "+id+" ")
	}
	r.keys(t, key('k')) // the dialog has the keys: the plugin's Key handler calls
	got := waitSaid(t, logs, "caller", "called key ")
	want := []string{
		"called mark true <nil>",
		"called boom false failed boom: mark.lua:3: it broke",
		"called undeclared false refused file.save is not in the manifest's calls",
		"called unknown false unknown lua.nope",
		"called plugin false refused plugin.caller.mark is a plugin's command, never another plugin's to run",
		"called key true <nil>",
	}
	var calls []string
	for _, s := range got {
		if strings.HasPrefix(s, "called ") {
			calls = append(calls, s)
		}
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("the answers:\n got %q\nwant %q", calls, want)
	}
	r.waitPage(t, "<1")
	r.waitPage(t, "<key")
	if waiting, _, _ := onLoop(r, func() *pluginRun { return r.h.plugins.running["caller"] }).callStats(); waiting != 0 {
		t.Fatalf("%d calls still wait", waiting)
	}
}

// rawTUI runs the raw plugin (it numbers its own calls) at protocol, and waits until it answers.
func rawTUI(t *testing.T, protocol int) (*running, string, *pluginRun) {
	t.Helper()
	dir := t.TempDir()
	install := installPlugin3
	if protocol == 2 {
		install = installPlugin2
	}
	install(t, dir, "raw", "service", "rawcalls", "calls = [\"lua.mark\"]\n[[commands]]\nid = \"ping\"\ntitle = \"Ping\"\n")
	if protocol == 2 { // calls is protocol 3's: a protocol-2 manifest has none
		path := filepath.Join(dir, "raw", "plugin.toml")
		b, _ := os.ReadFile(path)
		_ = os.WriteFile(path, []byte(strings.Replace(string(b), "calls = [\"lua.mark\"]\n", "", 1)), 0o644)
	}
	r, logs := callTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("raw", "ping"); return true })
	waitSaid(t, logs, "raw", "pong")
	r.s.WaitFor(t, "raw ready", func(string) bool { return onLoop(r, func() bool { return r.h.plugins.running["raw"].ready }) })
	return r, logs, onLoop(r, func() *pluginRun { return r.h.plugins.running["raw"] })
}

// heldLoop sends the raw plugin command while the loop is held, so the calls it sends wait, and
// releases the loop once until says they have all been read.
func heldLoop(t *testing.T, r *running, run *pluginRun, command string, until func(waiting, dupes, busy int) bool) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free)
	onLoop(r, func() bool {
		r.h.p.Post(func() { <-release }) // queued before anything the command makes the plugin send
		run.send(plugin.MethodCommand, plugin.CommandParams(command))
		return true
	})
	for deadline := time.Now().Add(20 * time.Second); !until(run.callStats()); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			w, d, b := run.callStats()
			t.Fatalf("the calls never arrived: %d waiting, %d reused, %d busy", w, d, b)
		}
	}
	free()
}

// TestACallReusingAWaitingNumberIsNeverAnswered (ADR 1791762474 r3): a host.call whose number is
// a call still running is a protocol violation: logged, run never, answered never. The waiting
// call gets its own answer, the only one with its number. A call with no number is dropped, and
// a malformed one with a number refused.
func TestACallReusingAWaitingNumberIsNeverAnswered(t *testing.T) {
	r, logs, run := rawTUI(t, 3)
	heldLoop(t, r, run, "dup", func(waiting, dupes, _ int) bool { return waiting == 3 && dupes == 1 })
	waitSaid(t, logs, "raw", "result 9 ")
	if got := results(t, logs, "raw"); !slices.Equal(got, []string{"result 7 true ", "result 8 true ",
		"result 9 false refused malformed: plugin: malformed parameters: host.call's args is a list of strings"}) {
		t.Fatalf("the results: %q, want 7's, 8's and 9's, once each", got)
	}
	r.waitPage(t, "<c")
	if text := r.editorText(); !strings.Contains(text, "<a") || strings.Contains(text, "<b") {
		t.Fatalf("the page: %q: want 7's mark, and never the reused 7's", text)
	}
	b, _ := os.ReadFile(filepath.Join(logs, "raw.log"))
	for _, want := range []string{"host.call 7 dropped, unanswered", "host.call dropped: plugin: malformed parameters: host.call wants a call number"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("%q is not in the log:\n%s", want, b)
		}
	}
}

// TestPastSixtyFourWaitingCallsTheHostIsBusy (§4.5): with 64 calls waiting, a 65th, a new number,
// is answered refused busy and runs nothing; every call is answered exactly once.
func TestPastSixtyFourWaitingCallsTheHostIsBusy(t *testing.T) {
	r, logs, run := rawTUI(t, 3)
	heldLoop(t, r, run, "busy", func(waiting, _, busy int) bool { return waiting == plugin.MaxPendingCalls && busy == 6 })
	r.s.WaitFor(t, "70 results", func(string) bool { return len(results(t, logs, "raw")) >= 70 })
	got := results(t, logs, "raw")
	seen, ok := map[string]bool{}, 0
	for _, s := range got {
		n, rest, _ := strings.Cut(strings.TrimPrefix(s, "result "), " ")
		if seen[n] {
			t.Fatalf("call %s answered twice: %q", n, got)
		}
		seen[n] = true
		switch rest {
		case "true ":
			ok++
		case "false refused busy":
		default:
			t.Fatalf("an answer %q", s)
		}
	}
	if len(got) != 70 || ok != plugin.MaxPendingCalls {
		t.Fatalf("%d results, %d ok: %q", len(got), ok, got)
	}
	r.s.WaitFor(t, "64 marks", func(string) bool { return strings.Count(r.editorText(), "<") == plugin.MaxPendingCalls })
}

// TestAProtocolTwoPluginsCallIsDropped: host.call is protocol 3's; a protocol-2 plugin's runs
// nothing and is answered nothing, and the plugin runs on.
func TestAProtocolTwoPluginsCallIsDropped(t *testing.T) {
	r, logs, run := rawTUI(t, 2)
	onLoop(r, func() bool { run.send(plugin.MethodCommand, plugin.CommandParams("once")); return true })
	r.s.WaitFor(t, "the call dropped", func(string) bool {
		b, _ := os.ReadFile(filepath.Join(logs, "raw.log"))
		return strings.Contains(string(b), "host.call dropped: the plugin speaks protocol 2")
	})
	onLoop(r, func() bool { run.send(plugin.MethodCommand, plugin.CommandParams("ping")); return true })
	r.s.WaitFor(t, "pong again", func(string) bool {
		return len(slices.DeleteFunc(said(t, logs, "raw"), func(s string) bool { return s != "pong" })) == 2
	})
	if got := results(t, logs, "raw"); len(got) != 0 || strings.Contains(r.editorText(), "<p>") {
		t.Fatalf("a protocol-2 call ran or was answered: %q, %q", got, r.editorText())
	}
}

// TestProtocolThreesCalls (§4.5): calls needs protocol = 3, exact ids, never a plugin's, each once;
// a protocol this AutoDoc does not speak is refused, as an older AutoDoc refuses protocol 3. The
// install question lists the calls.
func TestProtocolThreesCalls(t *testing.T) {
	const head = "name = \"p\"\nkind = \"dialog\"\ncommand = [\"x\"]\n"
	for _, c := range []struct{ toml, reason string }{
		{"protocol = 3\n" + head + "calls = [\"todos.open\", \"lua.mark\"]\n", ""},
		{"protocol = 3\n" + head, ""},
		{"protocol = 2\n" + head + "calls = [\"todos.open\"]\n", "declares calls: needs protocol = 3"},
		{"protocol = 3\n" + head + "calls = [\"todos.*\"]\n", `calls "todos.*": one exact command id, no wildcards`},
		{"protocol = 3\n" + head + "calls = [\"Todos\"]\n", `calls "Todos": not a command id`},
		{"protocol = 3\n" + head + "calls = [\"plugin.x.y\"]\n", `calls "plugin.x.y": a plugin's command is never`},
		{"protocol = 3\n" + head + "calls = [\"a.b\", \"a.b\"]\n", `calls "a.b": twice`},
		{"protocol = 4\n" + head, "protocol 4; this AutoDoc speaks 1 to 3"},
	} {
		dir := filepath.Join(t.TempDir(), "p")
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(c.toml), 0o644)
		e := readPlugin(dir)
		if c.reason == "" && e.reason != "" || c.reason != "" && !strings.HasPrefix(e.reason, c.reason) {
			t.Errorf("%q: reason %q, want %q", c.toml, e.reason, c.reason)
		}
		if c.reason == "" && strings.Contains(c.toml, "calls") {
			if d := pluginDeclares(&e.m); d != "may run AutoDoc's commands: todos.open, lua.mark" {
				t.Errorf("the install question declares %q", d)
			}
		}
	}
}
