package tui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/style"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// THE PLUGINS (ADR 0209) — the Plugins menu lists the plugins folder; a plugin's dialog shows its
// frames and takes its keys; Esc, a crash, silence or another protocol close it, and the close
// leaves nothing of the plugin running.
//
// The plugin is this test binary, run again: TestMain serves as one when its first argument is
// testPluginArg (the manifest's command).

const testPluginArg = "autodoc-test-plugin"

func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == testPluginArg {
		os.Exit(runTestPlugin(os.Args[2]))
	}
	os.Exit(m.Run())
}

// runTestPlugin is the test plugin, in one of its modes.
func runTestPlugin(mode string) int {
	switch mode {
	case "silent": // never answers
		select {}
	case "protocol2": // answers in another protocol
		conn := testStdio()
		done := make(chan struct{})
		var link *plugin.Link
		link, _ = plugin.NewLink(context.Background(), conn, func(m string, _ []any) {
			if m == plugin.MethodOpen {
				_ = link.Notify(context.Background(), plugin.MethodReady, plugin.ReadyParams(plugin.Protocol+1))
			}
			if m == plugin.MethodClose {
				close(done)
			}
		})
		<-done
		return 0
	case "stubborn": // ignores plugin.close and SIGTERM, and starts a child of its own
		signal.Ignore(syscall.SIGTERM)
		child := exec.Command("sleep", "60")
		_ = child.Start()
		fmt.Fprintf(os.Stderr, "child %d\n", child.Process.Pid)
	}
	e := &echo{mode: mode}
	if err := plugin.Serve(context.Background(), e); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		return 1
	}
	return 0
}

// testStdio is the plugin's stdin and stdout, pollable, as plugin.Serve makes them.
func testStdio() net.Conn {
	for _, fd := range []int{0, 1} {
		_ = syscall.SetNonblock(fd, true)
	}
	return plugin.FileConn(os.NewFile(0, "stdin"), os.NewFile(1, "stdout"))
}

// echo draws what it has been sent: its size and theme, and the keys, one row each.
type echo struct {
	mode  string
	mu    sync.Mutex
	peer  *plugin.Peer
	w, h  int
	lines []string
}

func (e *echo) Open(p *plugin.Peer, o plugin.Open) {
	e.peer, e.w, e.h = p, o.Width, o.Height
	e.say(fmt.Sprintf("open %dx%d %s %s", o.Width, o.Height, o.Theme.Name, o.Theme.Colors["document.cursor"]))
	if e.mode == "stubborn" {
		fmt.Fprintln(os.Stderr, "stubborn: opened")
	}
}

func (e *echo) Key(k plugin.Key) {
	switch k.Key {
	case "q":
		_ = e.peer.Close()
	case "x":
		fmt.Fprintln(os.Stderr, "boom: the test plugin crashed")
		os.Exit(3)
	case "t":
		_ = e.peer.Title("Titled")
	case "c":
		f := plugin.NewFrame(e.w, e.h)
		f.Text(0, 0, "RED", plugin.Style{FG: "red", BG: "#0000ff", Bold: true})
		f.Text(4, 0, "odd", plugin.Style{FG: "bleu"})
		_ = e.peer.Frame(f)
		return
	}
	e.say(fmt.Sprintf("key %s ctrl=%v", k.Key, k.Ctrl))
}

func (e *echo) Resize(w, h int) {
	e.w, e.h = w, h
	e.say(fmt.Sprintf("resize %dx%d", w, h))
}

func (e *echo) Theme(t plugin.Theme) { e.say("theme " + t.Name) }

func (e *echo) Close() {
	fmt.Fprintln(os.Stderr, "echo: closing")
	if e.mode == "stubborn" {
		select {} // never returns: the host must stop it
	}
}

func (e *echo) say(line string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lines = append(e.lines, line)
	f := plugin.NewFrame(e.w, e.h)
	for i, l := range e.lines[max(0, len(e.lines)-e.h):] {
		f.Text(0, i, l, plugin.Style{})
	}
	_ = e.peer.Frame(f)
}

// ---------------------------------------------------------------- the host's side

// installTestPlugin writes a plugin.toml for the test plugin in mode, in dir/<name>.
func installTestPlugin(t *testing.T, dir, name, mode string, w, h int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("name = %q\ntitle = %q\nkind = \"dialog\"\nprotocol = 1\ncommand = [%q, %q, %q]\n[dialog]\nwidth = %d\nheight = %d\n",
		name, strings.ToUpper(name[:1])+name[1:], exe, testPluginArg, mode, w, h)
	if err := os.WriteFile(filepath.Join(p, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shorten sets the plugin deadlines for a test and puts them back after it — registered before the
// TUI runs, so it is undone after the TUI's own cleanups.
func shorten(t *testing.T, handshake, grace, term time.Duration) {
	a, b, c := pluginHandshake, pluginGrace, pluginTermGrace
	pluginHandshake, pluginGrace, pluginTermGrace = handshake, grace, term
	t.Cleanup(func() { pluginHandshake, pluginGrace, pluginTermGrace = a, b, c })
}

// pluginTUI runs the TUI with the plugins in dir, and stops what it started when the test ends.
func pluginTUI(t *testing.T, dir string) (*running, string) {
	t.Helper()
	logs := t.TempDir()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{Plugins: Plugins{Dir: dir, LogDir: logs, Socket: d.sock}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	t.Cleanup(func() {
		runs := onLoop(r, func() []*pluginRun {
			var out []*pluginRun
			for _, p := range r.h.running {
				out = append(out, p)
			}
			return out
		})
		for _, p := range runs {
			p.shutdown()
		}
	})
	return r, logs
}

func (r *running) openPlugin(key string) *pluginRun {
	return onLoop(r, func() *pluginRun { r.h.openPlugin(key); return r.h.running[key] })
}

// waitShown waits for text on the screen, longer than a screen's usual wait: a plugin is a process
// to start, and under -race this test binary, which is the plugin, is slow to.
func (r *running) waitShown(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(r.s.String(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("%q never appeared:\n%s", text, r.s)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitNotice waits for a notification saying text: the history keeps it whole, where a toast wraps
// and goes.
func (r *running) waitNotice(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !onLoop(r, func() bool {
		for _, n := range r.h.notices {
			if strings.Contains(n.text, text) {
				return true
			}
		}
		return false
	}) {
		if time.Now().After(deadline) {
			t.Fatalf("no notification %q; the history: %v", text, onLoop(r, func() []notice { return r.h.notices }))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// TestThePluginsMenuListsThePluginsFolder: each plugin is an entry, one that cannot run is listed
// disabled with why, and with no plugins there is no Plugins menu.
func TestThePluginsMenuListsThePluginsFolder(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "echo", "echo", 30, 6)
	for name, toml := range map[string]string{
		"broken":  "name = \n",
		"future":  "name = \"future\"\nkind = \"dialog\"\nprotocol = 2\ncommand = [\"x\"]\n",
		"panel":   "name = \"panel\"\nkind = \"panel\"\nprotocol = 1\ncommand = [\"x\"]\n",
		"nocmd":   "name = \"nocmd\"\nkind = \"dialog\"\nprotocol = 1\n",
		"Upper":   "name = \"Upper\"\nkind = \"dialog\"\nprotocol = 1\ncommand = [\"x\"]\n",
		"z-echo2": "name = \"echo\"\nkind = \"dialog\"\nprotocol = 1\ncommand = [\"x\"]\n",
	} {
		_ = os.MkdirAll(filepath.Join(dir, name), 0o755)
		_ = os.WriteFile(filepath.Join(dir, name, "plugin.toml"), []byte(toml), 0o644)
	}
	_ = os.MkdirAll(filepath.Join(dir, "not-a-plugin"), 0o755)
	r, _ := pluginTUI(t, dir)
	r.keys(t, decltest.Alt('p'))
	r.s.WaitForText(t, "Echo")
	sc := r.s.String()
	for _, want := range []string{"broken — plugin.toml:", "future — protocol 2; this AutoDoc speaks 1",
		`panel — kind "panel": this AutoDoc runs dialog plugins`, "nocmd — no command",
		`Upper — name "Upper": want lower-case`, `echo — the name "echo" is echo's already`} {
		if !strings.Contains(sc, want) {
			t.Errorf("the menu lacks %q:\n%s", want, sc)
		}
	}
	if strings.Contains(sc, "not-a-plugin") {
		t.Errorf("a directory with no plugin.toml is listed:\n%s", sc)
	}
	rows := onLoop(r, func() int { return len(r.h.pluginList) })
	if rows != 7 {
		t.Errorf("%d plugins listed, want 7", rows)
	}
	r.keys(t, esc())

	none, _ := pluginTUI(t, t.TempDir())
	none.keys(t, f10())
	none.s.WaitForText(t, "System")
	if strings.Contains(none.s.String(), "Plugins") {
		t.Errorf("a Plugins menu with no plugins:\n%s", none.s.String())
	}
}

// TestAPluginsDialogShowsItsFramesAndTakesItsKeys: the menu starts the plugin; the dialog, titled,
// shows what it draws, told the size and the theme; keys reach it in order, Esc does not; a
// frame's colours are the theme vocabulary; a title it sets shows; Esc closes it and stops it.
func TestAPluginsDialogShowsItsFramesAndTakesItsKeys(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "echo", "echo", 30, 6)
	r, logs := pluginTUI(t, dir)
	r.keys(t, decltest.Alt('p'))
	r.s.WaitForText(t, "Echo")
	r.keys(t, enter())
	r.waitShown(t, "open 30x6 dark #ffaf00") // the size, and dark's cursor from its values
	if !strings.Contains(r.s.String(), "Echo · Esc closes") {
		t.Fatalf("the dialog is not titled:\n%s", r.s)
	}
	r.keys(t, key('a'), decltest.Ctrl('b'), tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyUp})
	r.s.WaitForText(t, "key Up ctrl=false")
	sc := r.s.String()
	if !strings.Contains(sc, "key a ctrl=false") || !strings.Contains(sc, "key b ctrl=true") ||
		strings.Index(sc, "key a") > strings.Index(sc, "key b") {
		t.Fatalf("the keys, in order:\n%s", sc)
	}
	r.keys(t, key('t'))
	r.s.WaitForText(t, "Titled · Esc closes")

	r.keys(t, key('c'))
	r.s.WaitForText(t, "RED odd")
	run := onLoop(r, func() *pluginRun { return r.h.running["echo"] })
	cells := onLoop(r, func() []pluginCell { run.mu.Lock(); defer run.mu.Unlock(); return run.frame[0] })
	if fg, _ := cells[0].st.GetForeground(); fg != style.ANSI(1) {
		t.Errorf("RED's colour = %v, want ANSI red", fg)
	}
	if bg, _ := cells[0].st.GetBackground(); bg != style.RGB(0, 0, 0xff) {
		t.Errorf("RED's background = %v, want #0000ff", bg)
	}
	pid := run.cmd.Process.Pid

	r.keys(t, esc())
	r.s.WaitFor(t, "the dialog closed", func(sc string) bool { return !strings.Contains(sc, "Esc closes") })
	select {
	case <-run.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the plugin did not exit after Esc")
	}
	if pidAlive(pid) {
		t.Errorf("the plugin %d is still running", pid)
	}
	log, _ := os.ReadFile(filepath.Join(logs, "echo.log"))
	if !strings.Contains(string(log), "echo: closing") || !strings.Contains(string(log), "1 unknown colours") {
		t.Errorf("the plugin's log:\n%s", log)
	}
	if n := onLoop(r, func() int { return len(r.h.running) }); n != 0 {
		t.Errorf("%d plugins still open", n)
	}
}

// TestAPluginThatFailsClosesItsDialogAndSaysWhy: a crash, silence, another protocol, a command that
// is not there, each closes the dialog with a toast naming the cause.
func TestAPluginThatFailsClosesItsDialogAndSaysWhy(t *testing.T) {
	shorten(t, 4*time.Second, 2*time.Second, time.Second)
	dir := t.TempDir()
	installTestPlugin(t, dir, "crash", "echo", 30, 6)
	installTestPlugin(t, dir, "silent", "silent", 30, 6)
	installTestPlugin(t, dir, "future", "protocol2", 30, 6)
	_ = os.MkdirAll(filepath.Join(dir, "gone"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "gone", "plugin.toml"),
		[]byte("name = \"gone\"\nkind = \"dialog\"\nprotocol = 1\ncommand = [\"./no-such-program\"]\n"), 0o644)
	r, _ := pluginTUI(t, dir)

	r.openPlugin("crash")
	r.waitShown(t, "open 30x6")
	r.keys(t, key('x'))
	r.waitNotice(t, "crash exited (exit status 3): boom: the test plugin crashed")

	r.openPlugin("silent")
	r.waitNotice(t, "silent did not answer in 4s")

	r.openPlugin("future")
	r.waitNotice(t, "future speaks protocol 2; this AutoDoc speaks 1")

	if r.openPlugin("gone") != nil {
		t.Error("a plugin that did not start is open")
	}
	r.waitNotice(t, "gone did not start")
	if n := onLoop(r, func() int { return len(r.h.running) }); n != 0 {
		t.Errorf("%d plugins still open after failing", n)
	}
}

// TestTheCloseStopsAStubbornPluginAndWhatItStarted: a plugin that ignores plugin.close and SIGTERM
// is killed after the deadlines, with the child it started, while it is busy — nothing of it runs.
func TestTheCloseStopsAStubbornPluginAndWhatItStarted(t *testing.T) {
	shorten(t, 5*time.Second, 200*time.Millisecond, 200*time.Millisecond)
	dir := t.TempDir()
	installTestPlugin(t, dir, "stubborn", "stubborn", 30, 6)
	r, logs := pluginTUI(t, dir)
	run := r.openPlugin("stubborn")
	r.waitShown(t, "open 30x6")
	var child int
	deadline := time.Now().Add(5 * time.Second)
	for child == 0 && time.Now().Before(deadline) {
		b, _ := os.ReadFile(filepath.Join(logs, "stubborn.log"))
		fmt.Sscanf(string(b), "child %d", &child)
		time.Sleep(10 * time.Millisecond)
	}
	if child == 0 || !pidAlive(child) {
		t.Fatalf("the plugin's child is not running (%d)", child)
	}
	pid := run.cmd.Process.Pid
	r.keys(t, esc())
	select {
	case <-run.shutDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the close did not end")
	}
	var ee *exec.ExitError
	if !errors.As(run.exitErr, &ee) || ee.ProcessState.String() != "signal: killed" {
		t.Errorf("the plugin ended with %v, want killed", run.exitErr)
	}
	time.Sleep(50 * time.Millisecond) // the child's reaping is init's
	for _, p := range []int{pid, child} {
		if pidAlive(p) && !zombie(p) {
			t.Errorf("process %d is still running", p)
		}
	}
}

// zombie says p has exited and waits for its parent to reap it: gone, for this test.
func zombie(p int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", p))
	if err != nil {
		return false
	}
	f := strings.Fields(string(b))
	return len(f) > 2 && f[2] == "Z"
}

// TestAPluginIsToldTheThemeAndTheSizeItGot: a theme switch reaches the open plugin, and a dialog
// larger than the screen is laid out smaller, and the plugin told so.
func TestAPluginIsToldTheThemeAndTheSizeItGot(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "echo", "echo", 200, 60)
	r, _ := pluginTUI(t, dir) // 100×30
	r.openPlugin("echo")
	r.waitShown(t, "resize ")
	w, h := onLoop(r, func() int { return r.h.running["echo"].view.laidW }), onLoop(r, func() int { return r.h.running["echo"].view.laidH })
	if w >= 200 || h >= 60 || !strings.Contains(r.s.String(), fmt.Sprintf("resize %dx%d", w, h)) {
		t.Fatalf("laid %dx%d; the screen:\n%s", w, h, r.s)
	}
	r.h.p.Post(func() { r.h.switchTheme("retro") })
	r.s.WaitForText(t, "theme retro")
}
