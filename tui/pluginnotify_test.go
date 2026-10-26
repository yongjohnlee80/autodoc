package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/autodoc/script"
)

// The cells of ADR 1792351123 §5: notifications from plugins (protocol 8) and Lua. The plugins are
// the test binary in mode "rawcalls", sending host.notify as a plugin that is not the SDK does.

const notifyCommands = "[[commands]]\nid = \"notifywith\"\ntitle = \"Notify\"\n[[commands]]\nid = \"badnotify\"\ntitle = \"Bad\"\n" +
	"[[commands]]\nid = \"manynotify\"\ntitle = \"Many\"\n[[commands]]\nid = \"ping\"\ntitle = \"Ping\"\n"

// shows reports whether a toast's text is on the screen now.
func shows(r *running, text string) bool { return strings.Contains(noticeText(r.s.String()), text) }

// kept counts the history's notices holding text.
func kept(r *running, text string) int {
	return onLoop(r, func() int {
		n := 0
		for _, it := range r.h.noticeLog.items {
			if strings.Contains(it.text, text) {
				n++
			}
		}
		return n
	})
}

// TestAPluginsNotices (§5.2-§5.4): a plain notice and an ongoing one, updated, ended, started again;
// two plugins' same id their own; a bad notice and a protocol-7 plugin's refused, logged without the
// message; past MaxActiveNotices refused; a plugin stopped ends its ongoing notices once.
func TestAPluginsNotices(t *testing.T) {
	dir := t.TempDir()
	installPluginAt(t, dir, "a", "service", "rawcalls", notifyCommands, 8)
	installPluginAt(t, dir, "b", "service", "rawcalls", notifyCommands, 8)
	installPluginAt(t, dir, "old", "service", "rawcalls", notifyCommands, 7)
	r, logs := pluginTUIWith(t, dir, map[string]string{prefSeconds: "1"}) // a toast ended lingers 1 s

	r.pluginCommand("a", "notifywith", "", "0", "plain from a")
	r.waitNotice(t, "plain from a")

	r.pluginCommand("a", "notifywith", "sync", "1", "a syncing")
	r.pluginCommand("b", "notifywith", "sync", "1", "b syncing")
	r.s.WaitFor(t, "both ongoing", func(string) bool { return shows(r, "a syncing") && shows(r, "b syncing") })
	r.pluginCommand("a", "notifywith", "sync", "0", "a synced")
	r.waitNotice(t, "a synced")
	if !shows(r, "b syncing") || kept(r, "b syncing") != 0 {
		t.Fatal("ending a's sync touched b's")
	}
	r.pluginCommand("a", "notifywith", "sync", "1", "a again")
	r.s.WaitFor(t, "a's sync started again", func(string) bool { return shows(r, "a again") })
	time.Sleep(2500 * time.Millisecond) // past the first end's linger: the new cycle stays (golib Toasts)
	if !shows(r, "a again") {
		t.Fatal("a's restarted sync went at its first end's deadline")
	}

	r.pluginCommand("a", "badnotify")
	r.pluginCommand("old", "notifywith", "", "0", "secret from old")
	// the host logs a refusal as it reads the link, the plugin its own lines on stderr: no line the
	// plugin says after is a barrier, so each refusal is waited for itself
	for name, wants := range map[string][]string{
		"a":   {"host.notify refused: plugin: a notice refused: message is not a string", "host.notify refused: plugin: a notice refused: an ongoing notice with no id"},
		"old": {"host.notify refused: the manifest's protocol 7 is before 8"},
	} {
		waitLogged(t, logs, name, wants...)
		if b, _ := os.ReadFile(filepath.Join(logs, name+".log")); strings.Contains(string(b), "secret from old") {
			t.Errorf("%s's log holds a refused notice's message", name)
		}
	}
	if kept(r, "secret from old") != 0 || shows(r, "secret from old") {
		t.Fatal("a protocol-7 plugin's notice showed")
	}

	r.pluginCommand("b", "manynotify")
	// the notices are taken on the loop in the order sent: once one past the cap is refused, every
	// one before it is held
	waitLogged(t, logs, "b", fmt.Sprintf("host.notify refused: more than %d ongoing notices", plugin.MaxActiveNotices))
	runB := runOf(r, "b") // on the loop, then its tracker on the loop: never one onLoop inside another
	if active := onLoop(r, func() int { return len(runB.active) }); active != plugin.MaxActiveNotices {
		t.Fatalf("b holds %d ongoing notices, want the cap %d (its sync and 15 tasks; the 17th refused)", active, plugin.MaxActiveNotices)
	}

	r.turnOff("b")
	waitExited(t, runB)
	r.s.WaitFor(t, "b's ongoing notices ended", func(string) bool { return kept(r, "B: stopped") > 0 })
	if n := kept(r, "B: stopped"); n != plugin.MaxActiveNotices {
		t.Fatalf("%d \"stopped\" in the history, want one per ongoing notice (%d)", n, plugin.MaxActiveNotices)
	}
}

// TestANoticeQueuedBeforeAStopPostsNothing (§5.4): acceptNotice from a run that is stopping, or is
// no longer the plugin's live run, posts nothing and tracks nothing.
func TestANoticeQueuedBeforeAStopPostsNothing(t *testing.T) {
	dir := t.TempDir()
	installPluginAt(t, dir, "a", "service", "rawcalls", notifyCommands, 8)
	r, logs := pluginTUI(t, dir)
	r.pluginCommand("a", "ping")
	waitSaid(t, logs, "a", "pong")
	old := runOf(r, "a")
	onLoop(r, func() bool {
		stale := &pluginRun{e: old.e, pl: old.pl, closing: false}
		r.h.plugins.acceptNotice(stale, plugin.Notice{Message: "from a replaced run", ID: "x", Ongoing: true})
		old.closing = true
		r.h.plugins.acceptNotice(old, plugin.Notice{Message: "from a stopping run", ID: "y", Ongoing: true})
		old.closing = false
		return true
	})
	if kept(r, "from a") != 0 || shows(r, "from a replaced run") || shows(r, "from a stopping run") {
		t.Fatal("a stale or stopping run posted")
	}
	if n := onLoop(r, func() int { return len(old.active) }); n != 0 {
		t.Fatalf("%d ids tracked from refused notices", n)
	}
}

// TestLuaNotices (§5.5): one argument as before; options post, update, end and start again under the
// lua namespace; a bad option raises an error; an engine with only WithNotify gets the message.
func TestLuaNotices(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	run := func(src string) error {
		return onLoop(r, func() error { return r.h.scripts.L.DoString(src) })
	}
	if err := run(`autodoc.notify("plain lua")`); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "plain lua")
	if err := run(`autodoc.notify("syncing", {id = "sync", ongoing = true})`); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "lua's ongoing", func(string) bool { return shows(r, "syncing") })
	if err := run(`autodoc.notify("synced", {id = "sync"})`); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "synced")
	if err := run(`autodoc.notify("again", {id = "sync", ongoing = true})`); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "lua's sync again", func(string) bool { return shows(r, "again") })
	for _, bad := range []string{`autodoc.notify("x", {id = 7})`, `autodoc.notify("x", {ongoing = "yes"})`,
		`autodoc.notify("x", {ongoing = true})`, `autodoc.notify("x", {id = "has space"})`} {
		if err := run(bad); err == nil {
			t.Fatalf("%s raised no error", bad)
		}
	}

	var got []string
	eng := script.New(widget.NewEditorCore(), script.WithNotify(func(s string) { got = append(got, s) }))
	if err := eng.L.DoString(`editor.notify("only notify", {id = "x", ongoing = true})`); err != nil || len(got) != 1 || got[0] != "only notify" {
		t.Fatalf("with WithNotify alone: %v %q", err, got)
	}
}
