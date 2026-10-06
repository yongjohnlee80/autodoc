package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
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
	case "otherprotocol": // answers in a protocol its manifest does not say
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
	case "gc": // collects garbage all the time, drawing a count: the SDK's pipes must survive it
		e := &echo{mode: mode}
		go func() {
			for i := 0; ; i++ {
				runtime.GC()
				e.mu.Lock()
				if e.peer != nil {
					f := plugin.NewFrame(e.w, e.h)
					f.Text(0, 0, fmt.Sprintf("tick %d", i), plugin.Style{})
					_ = e.peer.Frame(f)
				}
				e.mu.Unlock()
				time.Sleep(20 * time.Millisecond)
			}
		}()
		if err := plugin.Serve(context.Background(), e); err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			return 1
		}
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
	e.say(fmt.Sprintf("open %dx%d %s %s p%d", o.Width, o.Height, o.Theme.Name, o.Theme.Colors["document.cursor"], o.Protocol))
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

// protocol 2's: a card's focus, a command, the feed's document
func (e *echo) Focus(focused bool) { e.say(fmt.Sprintf("focus %v", focused)) }
func (e *echo) Command(id string)  { e.say("command " + id) }
func (e *echo) Document(d plugin.Document) {
	sel := ""
	for _, r := range d.Selection {
		sel += fmt.Sprintf(" sel %d:%d-%d:%d", r.Start.Line, r.Start.Col, r.End.Line, r.End.Col)
	}
	e.say(fmt.Sprintf("doc %q v%d %q at %d:%d%s large=%v", d.Path, d.Version, d.Text, d.Cursor.Line, d.Cursor.Col, sel, d.TooLarge))
}
func (e *echo) Hide() { e.say("hide") }
func (e *echo) Show() { e.say("show") }

func (e *echo) Close() {
	fmt.Fprintln(os.Stderr, "echo: closing")
	if e.mode == "stubborn" {
		select {} // never returns: the host must stop it
	}
}

func (e *echo) say(line string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintln(os.Stderr, "said: "+line) // a service has no surface: its log is where a test reads it
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

// installPlugin2 writes a protocol-2 manifest for the test plugin in mode: kind, then extra, the
// rest of the manifest (its tables).
func installPlugin2(t *testing.T, dir, name, kind, mode, extra string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("name = %q\ntitle = %q\nkind = %q\nprotocol = 2\ncommand = [%q, %q, %q]\n%s",
		name, strings.ToUpper(name[:1])+name[1:], kind, exe, testPluginArg, mode, extra)
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
	return pluginTUIWith(t, dir, nil)
}

// pluginTUIWith is pluginTUI with these preferences stored before the TUI starts.
func pluginTUIWith(t *testing.T, dir string, prefs map[string]string) (*running, string) {
	t.Helper()
	logs := t.TempDir()
	all := maps.Clone(testPrefs)
	maps.Copy(all, prefs)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: all})
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
// disabled with why, and a directory with no plugin.toml, or a clone being added, is not listed.
func TestThePluginsMenuListsThePluginsFolder(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "echo", "echo", 30, 6)
	for name, toml := range map[string]string{
		"broken":   "name = \n",
		"future":   "name = \"future\"\nkind = \"dialog\"\nprotocol = 3\ncommand = [\"x\"]\n",
		"panel":    "name = \"panel\"\nkind = \"panel\"\nprotocol = 1\ncommand = [\"x\"]\n",
		"nocmd":    "name = \"nocmd\"\nkind = \"dialog\"\nprotocol = 1\n",
		"Upper":    "name = \"Upper\"\nkind = \"dialog\"\nprotocol = 1\ncommand = [\"x\"]\n",
		"z-echo2":  "name = \"echo\"\nkind = \"dialog\"\nprotocol = 1\ncommand = [\"x\"]\n",
		"sideways": "name = \"sideways\"\nkind = \"dialog\"\nprotocol = 1\ncommand = [\"x\"]\n[dialog]\nplacements = [\"middle\"]\n",
		"escaper":  "name = \"escaper\"\nkind = \"dialog\"\nprotocol = 1\ncommand = [\"x\"]\n[dialog]\nesc = \"quit\"\n",
		"huge":     "name = \"huge\"\nkind = \"dialog\"\nprotocol = 1\ncommand = [\"x\"]\n[dialog]\nheight = \"150%\"\n",
	} {
		_ = os.MkdirAll(filepath.Join(dir, name), 0o755)
		_ = os.WriteFile(filepath.Join(dir, name, "plugin.toml"), []byte(toml), 0o644)
	}
	_ = os.MkdirAll(filepath.Join(dir, "not-a-plugin"), 0o755)
	r, _ := pluginTUI(t, dir)
	r.keys(t, decltest.Alt('p'))
	r.s.WaitForText(t, "Echo")
	sc := r.s.String()
	for _, want := range []string{"broken — plugin.toml:", "future — protocol 3; this AutoDoc speaks 1 to 2",
		`panel — kind "panel": want dialog or service`, "nocmd — no command",
		`Upper — name "Upper": want lower-case`, `echo — the name "echo" is echo's already`,
		`sideways — placement "middle": want center, top`, `escaper — esc "quit": want close or hide`,
		`huge — plugin.toml:`} {
		if !strings.Contains(sc, want) {
			t.Errorf("the menu lacks %q:\n%s", want, sc)
		}
	}
	if strings.Contains(sc, "not-a-plugin") {
		t.Errorf("a directory with no plugin.toml is listed:\n%s", sc)
	}
	rows := onLoop(r, func() int { return len(r.h.pluginList) })
	if rows != 10 {
		t.Errorf("%d plugins listed, want 10", rows)
	}
	r.keys(t, esc())

	// with none, the menu is its Add and Manage entries
	none, _ := pluginTUI(t, t.TempDir())
	none.keys(t, decltest.Alt('p'))
	none.s.WaitForText(t, "Manage plugins…")
	if !strings.Contains(none.s.String(), "no plugins yet") {
		t.Errorf("an empty folder's menu:\n%s", none.s)
	}
	if n := onLoop(none, func() int { return len(none.h.pluginList) }); n != 0 {
		t.Errorf("%d plugins in an empty folder", n)
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
	r.waitShown(t, "open 30x6 dark #ffaf00 p1") // the size, dark's cursor from its values, and the manifest's protocol
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
	// the plugins that answer get a handshake longer than a loaded runner's start of this test binary
	// (the plugin) under -race; only the one that never answers is held to the short deadline
	shorten(t, 30*time.Second, 2*time.Second, time.Second)
	dir := t.TempDir()
	installTestPlugin(t, dir, "crash", "echo", 30, 6)
	installTestPlugin(t, dir, "silent", "silent", 30, 6)
	installTestPlugin(t, dir, "future", "otherprotocol", 30, 6)
	_ = os.MkdirAll(filepath.Join(dir, "gone"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "gone", "plugin.toml"),
		[]byte("name = \"gone\"\nkind = \"dialog\"\nprotocol = 1\ncommand = [\"./no-such-program\"]\n"), 0o644)
	r, _ := pluginTUI(t, dir)

	r.openPlugin("crash")
	r.waitShown(t, "open 30x6")
	r.keys(t, key('x'))
	r.waitNotice(t, "crash exited (exit status 3): boom: the test plugin crashed")

	onLoop(r, func() bool { pluginHandshake = 4 * time.Second; return true }) // read as a plugin opens, on the loop
	r.openPlugin("silent")
	r.waitNotice(t, "silent did not answer in 4s")
	onLoop(r, func() bool { pluginHandshake = 30 * time.Second; return true })

	r.openPlugin("future")
	r.waitNotice(t, "future speaks protocol 3; its manifest says 1")

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

// appendManifest adds lines to a test plugin's manifest's [dialog] table, which it ends with.
func appendManifest(t *testing.T, dir, name, lines string) {
	t.Helper()
	path := filepath.Join(dir, name, "plugin.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, []byte(lines)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dialogEdges are the columns of the dialog titled title: its left and right corners, -1 when it
// is not on the screen.
func (r *running) dialogEdges(title string) (left, right int) {
	for _, row := range strings.Split(r.s.String(), "\n") {
		if i := strings.Index(row, "╭ "+title); i >= 0 {
			rs := []rune(row)
			l := len([]rune(row[:i]))
			for j := l; j < len(rs); j++ {
				if rs[j] == '╮' {
					return l, j
				}
			}
		}
	}
	return -1, -1
}

// TestADialogSitsWhereItsManifestSaysAndTheUserMovesIt: a plugin designed for the right and the
// left opens on the right, its first; Place moves it to the left at once and keeps the choice in the
// store; with no placements it is centred.
func TestADialogSitsWhereItsManifestSaysAndTheUserMovesIt(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "echo", "echo", 30, 6)
	installTestPlugin(t, dir, "side", "echo", 30, 6)
	appendManifest(t, dir, "side", "placements = [\"right\", \"left\"]\n")
	logs := t.TempDir()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{Plugins: Plugins{Dir: dir, LogDir: logs, Socket: d.sock}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	t.Cleanup(func() {
		for _, p := range onLoop(r, func() []*pluginRun {
			var out []*pluginRun
			for _, p := range r.h.running {
				out = append(out, p)
			}
			return out
		}) {
			p.shutdown()
		}
	})

	r.openPlugin("side")
	r.waitShown(t, "╭ Side")
	if _, right := r.dialogEdges("Side"); right != 99 {
		t.Fatalf("a right-first dialog ends at column %d, want 99 (the edge):\n%s", right, r.s)
	}
	r.h.p.Post(r.h.managePlugins)
	r.s.WaitFor(t, "the manager listed", func(string) bool { return onLoop(r, func() int { return len(r.h.managedList) }) == 2 })
	i := onLoop(r, func() int {
		for i, m := range r.h.managedList {
			if m.e.m.Name == "side" {
				return i
			}
		}
		return -1
	})
	r.h.p.Post(func() { r.h.placePlugin(i) })
	r.s.WaitFor(t, "the choice stored", func(string) bool {
		m, err := d.db.Preferences(context.Background())
		return err == nil && m["tui.plugin.side.placement"] == "left"
	})
	r.h.p.Post(func() { r.h.closeDialog("pluginManager") })
	r.s.WaitFor(t, "moved to the left", func(string) bool { l, _ := r.dialogEdges("Side"); return l == 0 })

	r.keys(t, esc())
	r.s.WaitFor(t, "side closed", func(string) bool { return onLoop(r, func() bool { return r.h.running["side"] == nil }) })
	r.openPlugin("echo")
	r.waitShown(t, "╭ Echo")
	if left, right := r.dialogEdges("Echo"); left < 30 || right > 69 {
		t.Fatalf("an unplaced dialog spans %d–%d, want centred:\n%s", left, right, r.s)
	}
}

// TestAStoredPlacementIsReadAtStart: the placement kept in the store is the dialog's from the
// start; one the manifest does not offer is ignored, and the first it offers is used.
func TestAStoredPlacementIsReadAtStart(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "side", "echo", 30, 6)
	appendManifest(t, dir, "side", "placements = [\"right\", \"left\"]\n")
	installTestPlugin(t, dir, "odd", "echo", 30, 6)
	appendManifest(t, dir, "odd", "placements = [\"right\", \"left\"]\n")
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{
		"tui.status.shown": "true", "tui.plugin.side.placement": "left", "tui.plugin.odd.placement": "bottom"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{Plugins: Plugins{Dir: dir, LogDir: t.TempDir(), Socket: d.sock}})
	r.s.WaitFor(t, "the stored placement read", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.pluginPlace["side"] == "left" })
	})
	t.Cleanup(func() {
		for _, p := range onLoop(r, func() []*pluginRun {
			var out []*pluginRun
			for _, p := range r.h.running {
				out = append(out, p)
			}
			return out
		}) {
			p.shutdown()
		}
	})
	r.openPlugin("side")
	r.waitShown(t, "╭ Side")
	if l, _ := r.dialogEdges("Side"); l != 0 {
		t.Fatalf("the stored left placement: the dialog starts at column %d:\n%s", l, r.s)
	}
	r.keys(t, esc())
	r.s.WaitFor(t, "side closed", func(string) bool { return onLoop(r, func() bool { return r.h.running["side"] == nil }) })
	r.openPlugin("odd")
	r.waitShown(t, "╭ Odd")
	if _, rt := r.dialogEdges("Odd"); rt != 99 {
		t.Fatalf("a stored placement the manifest does not offer is used (ends at %d):\n%s", rt, r.s)
	}
}

// TestEscHidesAPluginWithItsOwnQuit: esc = "hide" hides the dialog and tells the plugin, which
// runs on; its menu entry shows it again and tells it so; its own close ends it.
func TestEscHidesAPluginWithItsOwnQuit(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "game", "echo", 30, 6)
	appendManifest(t, dir, "game", "esc = \"hide\"\n")
	r, _ := pluginTUI(t, dir)
	run := r.openPlugin("game")
	r.waitShown(t, "Game · Esc hides")
	r.waitShown(t, "open 30x6")
	r.keys(t, esc())
	r.s.WaitFor(t, "hidden", func(sc string) bool { return !strings.Contains(sc, "Esc hides") })
	select {
	case <-run.exited:
		t.Fatal("Esc ended a plugin that hides")
	case <-time.After(300 * time.Millisecond):
	}
	if onLoop(r, func() *pluginRun { return r.h.running["game"] }) != run {
		t.Fatal("the hidden plugin is not kept")
	}
	r.openPlugin("game")
	r.waitShown(t, "show") // the plugin was told it was hidden, then shown
	if !strings.Contains(r.s.String(), "hide") {
		t.Fatalf("the plugin was not told it was hidden:\n%s", r.s)
	}
	r.keys(t, key('q')) // its own quit: host.close
	select {
	case <-run.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("its own quit did not end it")
	}
}

// TestAHeightInPercentIsTheScreensShare: height = "80%" lays the dialog out at 80% of the rows it
// may use, and the plugin is opened at that size.
func TestAHeightInPercentIsTheScreensShare(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "tall", "echo", 30, 6)
	b, _ := os.ReadFile(filepath.Join(dir, "tall", "plugin.toml"))
	_ = os.WriteFile(filepath.Join(dir, "tall", "plugin.toml"), []byte(strings.Replace(string(b), "height = 6", "height = \"80%\"", 1)), 0o644)
	r, _ := pluginTUI(t, dir) // 30 rows
	r.openPlugin("tall")
	r.waitShown(t, "open 30x")
	h := onLoop(r, func() int { return r.h.running["tall"].view.laidH })
	if h < 20 || h > 24 {
		t.Fatalf("an 80%% dialog is %d rows inside its border on a 30-row screen", h)
	}
	if !strings.Contains(r.s.String(), fmt.Sprintf("open 30x%d", h)) {
		t.Fatalf("the plugin was opened at another size than laid (%d):\n%s", h, r.s)
	}
}

// TestAPluginOutlivesGarbageCollection: a plugin that collects garbage keeps its pipes. The SDK
// once re-opened fd 1 and left the original os.Stdout unreferenced, so its finalizer closed the
// pipe at the next GC and the plugin ended some seconds in, cleanly and without a word (Johno:
// "exiting in around 10 seconds for no reason").
func TestAPluginOutlivesGarbageCollection(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "gc", "gc", 30, 6)
	r, _ := pluginTUI(t, dir)
	run := r.openPlugin("gc")
	r.waitShown(t, "tick 60") // dozens of collections in
	select {
	case <-run.exited:
		t.Fatalf("the plugin exited under garbage collection: %v", run.exitErr)
	default:
	}
}

// TestAPluginIsToldTheThemeAndTheSizeItGot: a theme switch reaches the open plugin, and a dialog
// larger than the screen is laid out smaller, and the plugin is opened at that size.
func TestAPluginIsToldTheThemeAndTheSizeItGot(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "echo", "echo", 200, 60)
	r, _ := pluginTUI(t, dir) // 100×30
	r.openPlugin("echo")
	r.waitShown(t, "open ")
	w, h := onLoop(r, func() int { return r.h.running["echo"].view.laidW }), onLoop(r, func() int { return r.h.running["echo"].view.laidH })
	// plugin.open waits for the first layout: the plugin is opened at the size it got, not asked
	if w >= 200 || h >= 60 || !strings.Contains(r.s.String(), fmt.Sprintf("open %dx%d", w, h)) {
		t.Fatalf("laid %dx%d; the screen:\n%s", w, h, r.s)
	}
	r.h.p.Post(func() { r.h.switchTheme("retro") })
	r.s.WaitForText(t, "theme retro")
}

// ---------------------------------------------------------------- adding and managing

// pluginRepo is a git repository holding the test plugin: its plugin.toml, whose build writes
// built.txt, and whose command is this test binary in echo mode.
func pluginRepo(t *testing.T, title, build string) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	commitPlugin(t, repo, title, build)
	return repo
}

// commitPlugin commits the test plugin's manifest into repo.
func commitPlugin(t *testing.T, repo, title, build string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("name = \"echo\"\ntitle = %q\nkind = \"dialog\"\nprotocol = 1\ncommand = [%q, %q, \"echo\"]\n[dialog]\nwidth = 30\nheight = 6\n[install]\nbuild = [\"sh\", \"-c\", %q]\n",
		title, exe, testPluginArg, build)
	if err := os.WriteFile(filepath.Join(repo, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "plugin.toml")
	runGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", title)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// question waits for PluginConfirm, and is what it asks.
func (r *running) question(t *testing.T) string {
	t.Helper()
	read := func() string {
		return onLoop(r, func() string {
			if r.h.pendingPlugin == nil {
				return ""
			}
			v, _ := r.h.p.Tree().Source("App.pluginQuestion")
			return v.Raw
		})
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if q := read(); q != "" && strings.Contains(r.s.String(), "Yes, at my own risk") {
			return q
		}
		if time.Now().After(deadline) {
			t.Fatalf("PluginConfirm never opened; the history: %v", onLoop(r, func() []notice { return r.h.notices }))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	ds, _ := os.ReadDir(dir)
	var out []string
	for _, d := range ds {
		out = append(out, d.Name())
	}
	return out
}

// TestAddingAPluginFromAGitURLAsksFirst: the clone runs nothing; the question names the source, the
// commit, the build and the command, with the warning; No throws the clone away; Yes builds it, puts
// it in the folder and in the Plugins menu, and it runs.
func TestAddingAPluginFromAGitURLAsksFirst(t *testing.T) {
	repo := pluginRepo(t, "Echo", "echo built > built.txt")
	commit := runGit(t, repo, "rev-parse", "--short", "HEAD")
	dir := t.TempDir()
	r, _ := pluginTUI(t, dir)
	url := "file://" + repo

	r.keys(t, decltest.Alt('p'))
	r.s.WaitForText(t, "Add from a git URL…")
	r.keys(t, key('a'))
	r.s.WaitForText(t, "the plugin's git repository")
	r.keys(t, decltest.Type(url)...)
	r.keys(t, enter())
	q := r.question(t)
	for _, want := range []string{"Echo (echo)", "from  " + url, "at    " + commit,
		"Its build runs:  sh -c echo built > built.txt", "It then starts:  ", "at your own risk"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question lacks %q:\n%s", want, q)
		}
	}
	if got := entries(t, dir); len(got) != 1 || !strings.HasPrefix(got[0], stagePrefix) {
		t.Fatalf("before the answer the folder holds %v, want one staged clone", got)
	}
	if _, err := os.Stat(filepath.Join(dir, entries(t, dir)[0], "built.txt")); err == nil {
		t.Fatal("the build ran before the question was answered")
	}
	if n := onLoop(r, func() int { return len(r.h.pluginList) }); n != 0 {
		t.Fatalf("a staged clone is listed as a plugin (%d)", n)
	}

	r.keys(t, key('n'))
	r.waitNotice(t, "echo not added")
	if got := entries(t, dir); len(got) != 0 {
		t.Fatalf("No left %v in the folder", got)
	}

	r.h.p.Post(func() { r.h.addPlugin(url) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at "+commit+": Plugins › Echo")
	if b, err := os.ReadFile(filepath.Join(dir, "echo", "built.txt")); err != nil || string(b) != "built\n" {
		t.Fatalf("the build did not run in the plugin's directory: %q, %v", b, err)
	}
	r.openPlugin("echo")
	r.waitShown(t, "open 30x6")
}

// TestWhatIsNotAPluginIsNotAdded: a URL git cannot clone, a repository with no plugin.toml, one that
// cannot run, one already installed and a build that fails are each refused, saying why, and leave
// nothing in the folder.
func TestWhatIsNotAPluginIsNotAdded(t *testing.T) {
	dir := t.TempDir()
	r, logs := pluginTUI(t, dir)
	add := func(url string) { r.h.p.Post(func() { r.h.addPlugin(url) }) }

	add("file:///no/such/repository")
	r.waitNotice(t, "not added: git clone:")

	empty := t.TempDir()
	runGit(t, empty, "init", "-q")
	_ = os.WriteFile(filepath.Join(empty, "README"), []byte("x"), 0o644)
	runGit(t, empty, "add", "README")
	runGit(t, empty, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x")
	add("file://" + empty)
	r.waitNotice(t, "not added: the repository has no plugin.toml at its top")

	future := t.TempDir()
	runGit(t, future, "init", "-q")
	_ = os.WriteFile(filepath.Join(future, "plugin.toml"), []byte("name = \"future\"\nkind = \"dialog\"\nprotocol = 3\ncommand = [\"x\"]\n"), 0o644)
	runGit(t, future, "add", "plugin.toml")
	runGit(t, future, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x")
	add("file://" + future)
	r.waitNotice(t, "not added: protocol 3; this AutoDoc speaks 1 to 2")

	failing := pluginRepo(t, "Echo", "echo nope >&2; exit 4")
	add("file://" + failing)
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "echo not installed: its build failed (exit status 4): nope")
	if got := entries(t, dir); len(got) != 0 {
		t.Fatalf("a refused plugin left %v in the folder", got)
	}
	if log, _ := os.ReadFile(filepath.Join(logs, "echo-install.log")); !strings.Contains(string(log), "nope") {
		t.Errorf("the install log: %q", log)
	}

	installTestPlugin(t, dir, "echo", "echo", 30, 6)
	add("file://" + pluginRepo(t, "Echo", "true"))
	r.waitNotice(t, "not added: echo is installed already: Manage plugins… updates it")
}

// TestAPluginIsUpdatedAndRemovedFromTheManager: the manager lists the plugin with its source and
// commit; Update… fetches, asks about the new commit's build, and moves to it; it says when there is
// nothing new; Remove… asks, then takes the directory away and the menu entry with it.
func TestAPluginIsUpdatedAndRemovedFromTheManager(t *testing.T) {
	repo := pluginRepo(t, "Echo", "echo one > built.txt")
	dir := t.TempDir()
	installTestPlugin(t, dir, "local", "echo", 30, 6)
	r, _ := pluginTUI(t, dir)
	r.h.p.Post(func() { r.h.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at")

	r.h.p.Post(r.h.managePlugins)
	r.s.WaitForText(t, "local: put in the folder by hand")
	r.s.WaitFor(t, "the clone's source listed", func(string) bool { // the column elides it on screen
		return onLoop(r, func() bool {
			for _, m := range r.h.managedList {
				if m.e.m.Name == "echo" && m.source == "file://"+repo && m.commit != "" {
					return true
				}
			}
			return false
		})
	})

	before := onLoop(r, func() []managedPlugin { return slices.Clone(r.h.managedList) })

	commitPlugin(t, repo, "Echo two", "echo two > built.txt")
	next := runGit(t, repo, "rev-parse", "--short", "HEAD")
	i := onLoop(r, func() int {
		for i, m := range r.h.managedList {
			if m.e.m.Name == "echo" {
				return i
			}
		}
		return -1
	})
	r.h.p.Post(func() { r.h.startUpdatePlugin(i) })
	if q := r.question(t); !strings.Contains(q, "at    "+next) || !strings.Contains(q, "echo two > built.txt") {
		t.Fatalf("the update's question is not the new commit's:\n%s", q)
	}
	r.keys(t, key('y'))
	r.waitNotice(t, "updated echo at "+next)
	if b, _ := os.ReadFile(filepath.Join(dir, "echo", "built.txt")); string(b) != "two\n" {
		t.Errorf("the update's build: %q", b)
	}
	r.s.WaitFor(t, "the new title listed", func(string) bool {
		return onLoop(r, func() bool {
			for _, e := range r.h.pluginList {
				if e.m.Title == "Echo two" {
					return true
				}
			}
			return false
		})
	})
	// Update again while the row still shows the commit before the update, as it does until the
	// manager's listing, run off the loop, comes back: up to date all the same.
	r.h.p.Post(func() { r.h.managedList = before; r.h.startUpdatePlugin(i) })
	r.waitNotice(t, "echo is up to date at "+next)

	r.h.p.Post(func() { r.h.startRemovePlugin(i) })
	r.s.WaitForText(t, "remove the plugin?")
	r.keys(t, key('y'))
	r.waitNotice(t, "removed echo")
	if _, err := os.Stat(filepath.Join(dir, "echo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plugin's directory is still there: %v", err)
	}
	if got := entries(t, dir); len(got) != 1 || got[0] != "local" {
		t.Fatalf("the folder holds %v, want the local plugin only", got)
	}
}

// TestProtocolTwosManifestKeys (ADR 1791268009 §1, §2.1): protocol 2's keys run only under
// protocol = 2, so no manifest half-works on an AutoDoc that speaks 1; an unknown key at any level
// disables the plugin, naming it; each key's own rules hold. A 0209 manifest still runs, and a valid
// card and service are listed enabled.
func TestProtocolTwosManifestKeys(t *testing.T) {
	const head = "kind = \"dialog\"\ncommand = [\"x\"]\n"
	const svc = "kind = \"service\"\nprotocol = 2\ncommand = [\"x\"]\n"
	for _, c := range []struct{ toml, reason string }{
		{"protocol = 1\n" + head, ""}, // 0209's
		{"protocol = 2\n" + head + "[dialog]\nmodal = false\nwidth = 30\n[feed]\ndocument = true\n[[commands]]\nid = \"toggle\"\ntitle = \"Toggle\"\nkey = \"s\"\n", ""},
		{svc + "start = \"launch\"\n", ""},
		{svc + "[feed]\ndocument = true\n", ""},
		{"protocol = 1\n" + head + "[feed]\ndocument = true\n", "declares [feed]: needs protocol = 2"},
		{"protocol = 1\n" + head + "[dialog]\nmodal = false\n", "declares [dialog] modal: needs protocol = 2"},
		{"protocol = 1\n" + head + "[[commands]]\nid = \"a\"\ntitle = \"A\"\n", "declares [[commands]]: needs protocol = 2"},
		{"protocol = 1\nkind = \"service\"\ncommand = [\"x\"]\nstart = \"launch\"\n", `declares kind = "service": needs protocol = 2`},
		{"protocol = 1\n" + head + "colour = \"red\"\n", `unknown key "colour"`},
		{"protocol = 1\n" + head + "[dialog]\ncolour = \"red\"\n", `unknown key "dialog.colour"`},
		{"protocol = 2\n" + head + "[feed]\ndiffs = true\n", `unknown key "feed.diffs"`},
		{"protocol = 2\n" + head + "[[commands]]\nid = \"a\"\ntitle = \"A\"\nletter = \"a\"\n", `unknown key "commands.letter"`},
		{svc + "start = \"launch\"\n[dialog]\nwidth = 30\n", "[dialog]: a service has no dialog"},
		{"protocol = 2\n" + head + "start = \"launch\"\n", "start: only a service starts on its own"},
		{svc + "start = \"boot\"\n", `start "boot": want use or launch`},
		{svc, `start = "use": nothing would start it (no command, no feed)`},
		{"protocol = 2\n" + head + "[dialog]\nmodal = false\nesc = \"hide\"\n", "[dialog] esc: a card's Esc gives the keys back"},
		{"protocol = 2\n" + head + "[[commands]]\nid = \"A\"\ntitle = \"A\"\n", `command id "A": want lower-case`},
		{"protocol = 2\n" + head + "[[commands]]\nid = \"a\"\ntitle = \"A\"\n[[commands]]\nid = \"a\"\ntitle = \"B\"\n", `command id "a": twice`},
		{"protocol = 2\n" + head + "[[commands]]\nid = \"a\"\n", `command "a": no title`},
		{"protocol = 2\n" + head + "[[commands]]\nid = \"a\"\ntitle = \"A\"\nkey = \"ab\"\n", `command "a": key "ab": want one letter or digit`},
		{"protocol = 2\n" + head + "[[commands]]\nid = \"a\"\ntitle = \"A\"\nkey = \"s\"\n[[commands]]\nid = \"b\"\ntitle = \"B\"\nkey = \"s\"\n", `command "b": key "s" is "a"'s already`},
	} {
		dir := filepath.Join(t.TempDir(), "p")
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte("name = \"p\"\n"+c.toml), 0o644)
		e := readPlugin(dir)
		if c.reason == "" && e.reason != "" || c.reason != "" && !strings.HasPrefix(e.reason, c.reason) {
			t.Errorf("%q: reason %q, want %q", c.toml, e.reason, c.reason)
		}
	}
}

// textAt is where text is on the screen: its column and row, -1 when it is not there.
func (r *running) textAt(text string) (x, y int) {
	for i, line := range strings.Split(r.s.String(), "\n") {
		if j := strings.Index(line, text); j >= 0 {
			return len([]rune(line[:j])), i
		}
	}
	return -1, -1
}

// TestACardTakesTheKeysOnlyWhenFocused (ADR 1791268009 §2.2): a non-modal card opens beside the
// page without taking the keys; a click on it focuses it, the plugin told plugin.focus true, and
// its keys go to the plugin; Esc gives them back to the editor, plugin.focus false, and the card
// stays open.
func TestACardTakesTheKeysOnlyWhenFocused(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", "[dialog]\nmodal = false\nwidth = 36\nheight = 8\nplacements = [\"top-right\"]\n")
	r, _ := pluginTUI(t, dir)
	r.openPlugin("card")
	r.waitShown(t, "open 36x8 dark")
	r.waitShown(t, "p2")
	r.s.WaitForText(t, "Card · Esc returns the keys")

	r.keys(t, key('i'), key('Q'))
	r.s.WaitFor(t, "the editor took the keys", func(string) bool { return strings.Contains(r.editorText(), "Q") })
	if strings.Contains(r.s.String(), "key Q") {
		t.Fatalf("a card that was not focused took a key:\n%s", r.s)
	}
	r.keys(t, esc())

	x, y := r.textAt("open 36x8")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.waitShown(t, "focus true")
	r.keys(t, key('z'))
	r.waitShown(t, "key z")
	before := r.editorText()

	r.keys(t, esc())
	r.waitShown(t, "focus false")
	if !strings.Contains(r.s.String(), "Card · Esc returns the keys") {
		t.Fatalf("Esc closed the card:\n%s", r.s)
	}
	r.keys(t, key('i'), key('W'))
	r.s.WaitFor(t, "the editor has the keys again", func(string) bool { return strings.Contains(r.editorText(), "W") })
	if strings.Contains(r.s.String(), "key W") || !strings.Contains(before, "Q") {
		t.Fatalf("after Esc the card still took keys:\n%s", r.s)
	}
}

// TestTheHostKeepsOnlyTheNewestDocumentWaiting (ADR 1791268009 §2.3): with the plugin reading
// nothing, 50 documents among keys and commands leave one document waiting, the newest, at the
// tail of what came before it; the keys and commands all wait, in order. Documents never count
// toward the queue's bound, and anything else past it is a plugin not reading.
func TestTheHostKeepsOnlyTheNewestDocumentWaiting(t *testing.T) {
	q := newOutQueue()
	var want []string
	for i := 1; i <= 50; i++ {
		if !q.push(outNote{plugin.MethodDocument, plugin.DocumentParams(plugin.Document{Path: "a.md", Version: i})}) {
			t.Fatalf("document %d refused", i)
		}
		// the newer document takes the tail as it is sent; the one waiting goes
		want = append(slices.DeleteFunc(want, func(s string) bool { return strings.HasPrefix(s, "document") }),
			fmt.Sprintf("document v%d", i))
		k := outNote{plugin.MethodKey, plugin.KeyParams(plugin.Key{Key: fmt.Sprint(i)})}
		c := outNote{plugin.MethodCommand, plugin.CommandParams(fmt.Sprintf("c%d", i))}
		q.push(k)
		q.push(c)
		want = append(want, "key "+fmt.Sprint(i), fmt.Sprintf("command c%d", i))
	}
	if all, docs := q.waiting(); all != 101 || docs != 1 {
		t.Fatalf("%d waiting, %d documents; want 101 and 1", all, docs)
	}
	q.close()
	var got []string
	for {
		n, ok := q.pop()
		if !ok {
			break
		}
		switch n.method {
		case plugin.MethodDocument:
			d, _ := plugin.ReadDocument(n.params)
			got = append(got, fmt.Sprintf("document v%d", d.Version))
		case plugin.MethodKey:
			k, _ := plugin.ReadKey(n.params)
			got = append(got, "key "+k.Key)
		case plugin.MethodCommand:
			id, _ := plugin.ReadCommand(n.params)
			got = append(got, "command "+id)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sent in the order\n%q\nwant\n%q", got, want)
	}

	full := newOutQueue()
	for i := 0; i < pluginQueue; i++ {
		if !full.push(outNote{plugin.MethodKey, nil}) {
			t.Fatalf("key %d refused under the bound", i)
		}
	}
	if !full.push(outNote{plugin.MethodDocument, nil}) {
		t.Error("a document refused at the bound: documents do not count toward it")
	}
	if full.push(outNote{plugin.MethodKey, nil}) {
		t.Error("a key past the bound was taken")
	}
}

// TestPluginCommandsOnTheSpcPCard (ADR 1791268009 §2.5): SPC p lists every plugin command with its
// letter, built from the host's model (a Repeater of Shortcuts in the card); a letter runs its
// command, starting the plugin first and sending the command once it has opened, and a card's
// command focuses it. Two plugins wanting one letter: the first by name keeps it, the other is
// listed unbound with why. A card's toggle is the host's: it closes the open card, and opens it
// again, focused; it is never sent to the plugin.
func TestPluginCommandsOnTheSpcPCard(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "alpha", "dialog", "echo", "[dialog]\nmodal = false\nwidth = 36\nheight = 8\n"+
		"[[commands]]\nid = \"toggle\"\ntitle = \"Show the card\"\nkey = \"s\"\n"+
		"[[commands]]\nid = \"hello\"\ntitle = \"Say hello\"\nkey = \"h\"\n")
	installPlugin2(t, dir, "beta", "dialog", "echo", "[dialog]\nwidth = 36\nheight = 8\n"+
		"[[commands]]\nid = \"x\"\ntitle = \"Do x\"\nkey = \"s\"\n")
	// gamma's folder is listed first, but alpha comes first by name: alpha keeps h
	installPlugin2(t, dir, "gamma", "dialog", "echo", "[[commands]]\nid = \"g\"\ntitle = \"Do g\"\nkey = \"h\"\n")
	if err := os.Rename(filepath.Join(dir, "gamma"), filepath.Join(dir, "0-gamma")); err != nil {
		t.Fatal(err)
	}
	r, _ := pluginTUI(t, dir)

	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugin commands")
	for _, want := range []string{"s  Alpha: Show the card", "h  Alpha: Say hello", "Beta: Do x — s is alpha's",
		"Gamma: Do g — h is alpha's"} {
		r.s.WaitForText(t, want)
	}
	r.keys(t, key('h')) // alpha is not running: started, then told
	r.waitShown(t, "command hello")
	r.waitShown(t, "focus true") // a card's command focuses it
	r.keys(t, key('z'))
	r.waitShown(t, "key z")

	r.keys(t, esc()) // the keys back to the editor; the card stays
	r.waitShown(t, "focus false")
	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugin commands")
	r.keys(t, key('s')) // toggle, the card open: closed
	r.s.WaitFor(t, "the card closed", func(sc string) bool { return !strings.Contains(sc, "Alpha · Esc returns the keys") })
	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugin commands")
	r.keys(t, key('s')) // toggle, the card closed: opened, focused
	r.waitShown(t, "focus true")
	if strings.Contains(r.s.String(), "command toggle") {
		t.Fatalf("the host's toggle was sent to the plugin:\n%s", r.s)
	}
	r.keys(t, key('y'))
	r.waitShown(t, "key y")
}

// TestAPreferenceRebindsAPluginCommand: tui.plugin.<name>.key.<id> overrides the manifest's letter,
// so the loser of a letter can be given another; "" unbinds a command.
func TestAPreferenceRebindsAPluginCommand(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "alpha", "dialog", "echo", "[dialog]\nwidth = 36\nheight = 8\n"+
		"[[commands]]\nid = \"a\"\ntitle = \"Do a\"\nkey = \"s\"\n"+
		"[[commands]]\nid = \"hello\"\ntitle = \"Say hello\"\nkey = \"h\"\n")
	installPlugin2(t, dir, "beta", "dialog", "echo", "[dialog]\nwidth = 36\nheight = 8\n"+
		"[[commands]]\nid = \"x\"\ntitle = \"Do x\"\nkey = \"s\"\n")
	r, _ := pluginTUIWith(t, dir, map[string]string{"tui.plugin.beta.key.x": "b", "tui.plugin.alpha.key.hello": ""})
	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugin commands")
	for _, want := range []string{"s  Alpha: Do a", "b  Beta: Do x", "Alpha: Say hello — unbound in the preferences"} {
		r.s.WaitForText(t, want)
	}
	r.keys(t, key('b'))
	r.waitShown(t, "command x")
}

// said is what the test plugin named has said so far, from its log: each line it drew, in order.
func said(t *testing.T, logs, name string) []string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(logs, name+".log"))
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if s, ok := strings.CutPrefix(l, "said: "); ok {
			out = append(out, s)
		}
	}
	return out
}

// waitSaid waits until the plugin has said a line starting with prefix, and returns what it said.
func waitSaid(t *testing.T, logs, name, prefix string) []string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		got := said(t, logs, name)
		if slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, prefix) }) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never said %q; it said %q", name, prefix, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func docsIn(lines []string) []string {
	return slices.DeleteFunc(slices.Clone(lines), func(s string) bool { return !strings.HasPrefix(s, "doc ") })
}

// TestTheDocumentFeed (ADR 1791268009 §2.3): a plugin that declares the feed is sent the note when
// it starts, then once per pause in editing: the editor's own text, unsaved, with the cursor and the
// selection in its columns (grapheme clusters, from 1), the version rising with each edit. A note too
// large for the link is sent too large. A plugin that did not declare the feed is never sent it.
func TestTheDocumentFeed(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "stats", "dialog", "echo", "[dialog]\nmodal = false\nwidth = 60\nheight = 6\n[feed]\ndocument = true\n")
	installPlugin2(t, dir, "quiet", "dialog", "echo", "[dialog]\nmodal = false\nwidth = 30\nheight = 4\nplacements = [\"bottom-left\"]\n")
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.openPlugin("stats"); r.h.openPlugin("quiet"); return true })
	first := docsIn(waitSaid(t, logs, "stats", "doc "))
	if len(first) != 1 || !strings.HasPrefix(first[0], `doc "" v`) || !strings.Contains(first[0], `"" at 1:1 large=false`) {
		t.Fatalf("the document at start: %q", first)
	}
	onLoop(r, func() bool { r.h.p.Call("editor", "forceActiveFocus"); return true })

	r.keys(t, key('i'), key('h'), key('é'), key('l'), key('l'), key('o'), esc())
	got := docsIn(waitSaid(t, logs, "stats", `doc "" v`+fmt.Sprint(onLoop(r, func() int { return r.h.feedVersion }))))
	time.Sleep(2 * pluginFeedDelay) // nothing more comes of that burst
	got = docsIn(said(t, logs, "stats"))
	if len(got) != 2 || !strings.Contains(got[1], `"héllo" at 1:5 large=false`) {
		t.Fatalf("one document for the burst, the text typed and the cursor on o (column 5): %q", got)
	}
	v1, v2 := version(t, got[0]), version(t, got[1])
	if v2 <= v1 {
		t.Fatalf("the version did not rise: %d then %d", v1, v2)
	}

	r.keys(t, key('v'), key('h'), key('h')) // the selection: é, l, l — columns 2 to 4, its end after it
	got = docsIn(waitSaid(t, logs, "stats", `doc "" v`+fmt.Sprint(v2)+` "héllo" at 1:3 sel 1:3-1:6`))
	r.keys(t, esc())

	big := strings.Repeat("x", plugin.MaxMessageBytes)
	onLoop(r, func() bool { r.h.editor.SetValue(big); r.h.edited(); return true })
	waitSaid(t, logs, "stats", `doc "" v`+fmt.Sprint(v2+1)+` "" at 0:0 large=true`)

	if q := docsIn(said(t, logs, "quiet")); len(q) != 0 {
		t.Fatalf("a plugin that did not declare the feed was sent it: %q", q)
	}
}

func version(t *testing.T, doc string) int {
	t.Helper()
	var v int
	if _, err := fmt.Sscanf(doc[strings.Index(doc, " v")+2:], "%d", &v); err != nil {
		t.Fatalf("no version in %q", doc)
	}
	return v
}

// TestServicesStartOnUseOrAtLaunch (ADR 1791268009 §2.4): a service has no surface. One that starts
// on use is not running until its command, which it is then sent, or the first event of its feed;
// one that starts at launch runs with the TUI. Each is opened with no size.
func TestServicesStartOnUseOrAtLaunch(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "oncmd", "service", "echo", "[[commands]]\nid = \"run\"\ntitle = \"Run\"\nkey = \"r\"\n")
	installPlugin2(t, dir, "onfeed", "service", "echo", "[feed]\ndocument = true\n")
	installPlugin2(t, dir, "atstart", "service", "echo", "start = \"launch\"\n[[commands]]\nid = \"run\"\ntitle = \"Run\"\n")
	r, logs := pluginTUI(t, dir)
	waitSaid(t, logs, "atstart", "open 0x0 dark")
	if running := onLoop(r, func() []string { return slices.Sorted(maps.Keys(r.h.running)) }); !reflect.DeepEqual(running, []string{"atstart"}) {
		t.Fatalf("running at start: %v, want atstart alone", running)
	}

	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugin commands")
	r.keys(t, key('r'))
	got := waitSaid(t, logs, "oncmd", "command run")
	if !reflect.DeepEqual(got[len(got)-1:], []string{"command run"}) || !strings.HasPrefix(got[0], "open 0x0 dark") {
		t.Fatalf("oncmd said %q: want it opened with no size, then the command", got)
	}
	if strings.Contains(r.s.String(), "open 0x0") {
		t.Fatalf("a service drew on the screen:\n%s", r.s)
	}

	if onLoop(r, func() bool { return r.h.running["onfeed"] != nil }) {
		t.Fatal("onfeed runs before its feed's first event")
	}
	r.keys(t, key('i'), key('k'), esc())
	waitSaid(t, logs, "onfeed", `doc "" v`)
}
