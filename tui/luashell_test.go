//go:build linux || darwin

package tui

import (
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/script"
)

// luashell_test.go: the Lua shell (ADR 1792256103 §5). The unit cells drive a luaShell with a loop
// of the test's own: each post waits in the test's queue until the test runs it, on the test's
// goroutine, the only one touching the engine. Every wait is for an observed state, never a sleep.

// shellRig is a luaShell, its engine and page, the loop's queue and what the shell has written.
type shellRig struct {
	t    *testing.T
	h    *Host
	core *widget.EditorCore
	s    *luaShell
	mu   sync.Mutex
	fns  []func()
	out  strings.Builder
	ping chan struct{} // a post or some output arrived
}

func newShellRig(t *testing.T, page string) *shellRig {
	t.Helper()
	core := widget.NewEditorCore(widget.CoreInitialText(page))
	eng := script.New(core, script.WithGlobal("autodoc"), script.WithTimeout(300*time.Millisecond))
	t.Cleanup(eng.Close)
	r := &shellRig{t: t, h: &Host{scripts: eng}, core: core, ping: make(chan struct{}, 1)}
	r.s = newLuaShell(r.h, r.post)
	return r
}

// start runs the shell and reads what it writes, until it ends.
func (r *shellRig) start() {
	go r.s.run()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.s.Read(buf)
			r.mu.Lock()
			r.out.Write(buf[:n])
			r.mu.Unlock()
			r.poke()
			if err != nil {
				return
			}
		}
	}()
	r.t.Cleanup(func() { _ = r.s.Close(); r.s.Wait() })
}

func (r *shellRig) poke() {
	select {
	case r.ping <- struct{}{}:
	default:
	}
}

func (r *shellRig) post(fn func()) {
	r.mu.Lock()
	r.fns = append(r.fns, fn)
	r.mu.Unlock()
	r.poke()
}

func (r *shellRig) posts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.fns)
}

func (r *shellRig) output() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.out.String()
}

// until waits for cond, checking at each post or output.
func (r *shellRig) until(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(2 * time.Millisecond) // a state no post or output marks (admit's count)
	defer tick.Stop()
	for !cond() {
		select {
		case <-r.ping:
		case <-tick.C:
		case <-deadline:
			r.t.Fatalf("%s never came; the shell wrote %q", what, r.output())
		}
	}
}

// runPost runs the oldest post, as the loop would.
func (r *shellRig) runPost() {
	r.t.Helper()
	r.until("a post", func() bool { return r.posts() > 0 })
	r.mu.Lock()
	fn := r.fns[0]
	r.fns = r.fns[1:]
	r.mu.Unlock()
	fn()
}

func (r *shellRig) write(text string) {
	r.t.Helper()
	if _, err := r.s.Write([]byte(text)); err != nil {
		r.t.Fatalf("write %q: %v", text, err)
	}
}

// shellGlobal is name in the shell's environment, as the shell itself reads it ("nil" for none).
func (r *shellRig) shellGlobal(name string) string {
	if r.s.sh == nil {
		return "nil"
	}
	vals, err := r.s.eng.Eval(r.s.sh, name)
	if err != nil || len(vals) != 1 {
		r.t.Fatalf("reading %s: %v %v", name, vals, err)
	}
	return vals[0]
}

// R2: an incomplete chunk's answer held while its continuation, a paste and Enter are typed. On
// release, the continuation joins the pending lines and runs once, then the paste runs once; no key
// is lost or reordered and no line runs alone. A shell applying keys while evaluating would send
// "print(i) end" alone: a problem, which this cell sees.
func TestTheShellHoldsKeysUntilTheAnswer(t *testing.T) {
	r := newShellRig(t, "")
	r.start()
	r.write("for i=1,2 do\r")
	r.until("the first request", func() bool { return r.posts() == 1 })
	r.write("print(i) end\r")
	r.write("\x1b[200~x = 1\ny = 2\x1b[201~\r")
	if r.posts() != 1 {
		t.Fatalf("%d requests while one was in flight", r.posts())
	}
	r.runPost() // incomplete: the continuation is applied after it
	r.until("the joined chunk's request", func() bool { return r.posts() == 1 })
	r.runPost() // the loop prints 1 and 2
	r.until("the loop's prints", func() bool { return strings.Contains(r.output(), "1\r\n2\r\n") })
	r.until("the paste's request", func() bool { return r.posts() == 1 })
	r.runPost()
	r.until("the last prompt", func() bool { return strings.HasSuffix(r.output(), "\r\x1b[K> ") })
	if r.posts() != 0 {
		t.Fatalf("%d more requests", r.posts())
	}
	out := r.output()
	if strings.Contains(out, "\x1b[31m") {
		t.Fatalf("a problem: a line ran alone: %q", out)
	}
	if strings.Count(out, "1\r\n2\r\n") != 1 {
		t.Fatalf("the loop ran %d times: %q", strings.Count(out, "1\r\n2\r\n"), out)
	}
	if r.shellGlobal("x") != "1" || r.shellGlobal("y") != "2" {
		t.Fatalf("the paste: x %v y %v", r.shellGlobal("x"), r.shellGlobal("y"))
	}
}

// R3: a request posted, then the tab closed, then the request run by the loop: no Eval, no edit of
// the page, no environment made; dropShell then has nothing to forget. A shell without the closed
// check at execution edits the page and makes an environment.
func TestAClosedShellRunsNothingQueued(t *testing.T) {
	r := newShellRig(t, "page")
	r.start()
	r.write(`ed:insert_at(1, 1, "Z")` + "\r")
	r.until("the request", func() bool { return r.posts() == 1 })
	_ = r.s.Close()
	r.runPost() // the request, after the close
	if r.core.Value() != "page" || r.s.sh != nil || r.s.eng != nil {
		t.Fatalf("a closed shell's request ran: page %q, environment %v", r.core.Value(), r.s.sh != nil)
	}
	r.runPost() // dropShell
	if r.s.sh != nil {
		t.Fatal("dropShell made an environment")
	}
	if n, err := r.s.Wait(); n != 0 || err != nil {
		t.Fatalf("Wait: %d %v", n, err)
	}
}

// R3a: Wait answers once run itself has returned, not when Close asked it to; then every Wait, from
// any goroutine, answers the same. A Wait reading the stop request returns while run is held.
func TestWaitIsRunsEnd(t *testing.T) {
	r := newShellRig(t, "")
	entered, release := make(chan struct{}), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	r.s.held = func() { close(entered); <-release }
	r.start()
	t.Cleanup(releaseOnce) // runs before start's Close and Wait: a failed cell never hangs there
	r.write("1\r")
	r.runPost()
	<-entered // run is inside settle, held
	_ = r.s.Close()
	waited := make(chan int, 4)
	for range 4 {
		go func() { n, _ := r.s.Wait(); waited <- n }()
	}
	// held, run cannot return: a Wait that answers in this window answered the stop request, not
	// run's end. A Wait reading done answers at once; this one never answers before release.
	select {
	case <-waited:
		t.Fatal("Wait returned while run was still held")
	case <-time.After(200 * time.Millisecond):
	}
	releaseOnce()
	for range 4 {
		if n := <-waited; n != 0 {
			t.Fatalf("Wait answered %d", n)
		}
	}
	if n, err := r.s.Wait(); n != 0 || err != nil {
		t.Fatalf("a Wait after: %d %v", n, err)
	}
	_ = r.s.Close() // a second Close: nothing
}

// Ctrl+D on an empty line closes the shell from run itself; a Close before run's first step ends it
// at once. Wait returns in both.
func TestTheShellClosesItself(t *testing.T) {
	r := newShellRig(t, "")
	r.start()
	r.until("the prompt", func() bool { return strings.HasSuffix(r.output(), "> ") })
	r.write("\x04")
	r.s.Wait()
	if !r.s.closed.Load() {
		t.Fatal("Ctrl+D did not close the shell")
	}
	early := newShellRig(t, "")
	_ = early.s.Close()
	early.start()
	early.s.Wait()
	if strings.Contains(early.output(), "Lua 5.1") {
		t.Fatal("a shell closed before it began drew its banner")
	}
}

// R3b: once Close has run, a Write is refused before its bytes are admitted, while keys could take
// them; a Write blocked on keys when Close comes is freed. Without Write's closed check, admit sees
// the Write, whichever case its select then takes.
func TestWriteIsRefusedAfterClose(t *testing.T) {
	r := newShellRig(t, "")
	entered, release := make(chan struct{}), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	r.s.held = func() { close(entered); <-release }
	var admitted atomic.Int32
	r.s.admit = func() { admitted.Add(1) }
	r.start()
	t.Cleanup(releaseOnce) // runs before start's Close and Wait: a failed cell never hangs there
	r.write("1\r")
	r.runPost()
	<-entered // run is held: nothing takes keys
	blocked := make(chan error, 1)
	go func() { _, err := r.s.Write([]byte("Q")); blocked <- err }()
	r.until("the blocked writer admitted", func() bool { return admitted.Load() == 2 })
	_ = r.s.Close()
	if err := <-blocked; !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("the blocked Write: %v", err)
	}
	before := admitted.Load()
	if _, err := r.s.Write([]byte("W")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("a Write after Close: %v", err)
	}
	if admitted.Load() != before {
		t.Fatal("a Write after Close was admitted")
	}
	releaseOnce()
	r.s.Wait()
	if strings.Contains(r.output(), "Q") || strings.Contains(r.output(), "W") {
		t.Fatalf("keys after Close were drawn: %q", r.output())
	}
}

// Lector r2: a Close inside the held batch (from the held hook) and a Ctrl+D among the held keys
// stop the batch there: no request for the keys after, no output after.
func TestCloseInsideTheHeldBatch(t *testing.T) {
	for _, viaEOF := range []bool{false, true} {
		r := newShellRig(t, "")
		if !viaEOF {
			r.s.held = func() { _ = r.s.Close() }
		}
		r.start()
		r.write("1\r")
		r.until("the request", func() bool { return r.posts() == 1 })
		if viaEOF {
			r.write("\x04y = 1\r") // held: Ctrl+D on the empty line, then a line that must never run
		} else {
			r.write("y = 1\r")
		}
		r.runPost()
		r.s.Wait()
		for r.posts() > 0 {
			r.runPost() // dropShell only: a request here would run y = 1
		}
		if r.shellGlobal("y") != "nil" {
			t.Fatalf("eof %v: a key after the close ran", viaEOF)
		}
	}
}

// A request queued while the scripts reload runs on the new engine, after saying so; the old
// engine's globals are gone with it.
func TestAReloadBetweenRequests(t *testing.T) {
	r := newShellRig(t, "")
	r.start()
	r.write("a = 1\r")
	r.runPost()
	r.write("a\r")
	r.until("the second request", func() bool { return r.posts() == 1 })
	old := r.h.scripts
	r.h.scripts = script.New(widget.NewEditorCore(), script.WithGlobal("autodoc"))
	t.Cleanup(r.h.scripts.Close)
	r.runPost()
	r.until("the answer", func() bool { return strings.Contains(r.output(), "nil\r\n") })
	if !strings.Contains(r.output(), "the scripts were reloaded") || r.s.eng == old {
		t.Fatalf("the reload: %q", r.output())
	}
}

// The decoder reads a rune and a sequence split across Writes as one key each, and a paste split
// across Writes as one paste.
func TestTheShellDecodesSplitKeys(t *testing.T) {
	var d shellDecoder
	var got []shellKey
	for _, b := range []string{"\xc3", "\xa9", "\x1b", "[A", "\x1b[20", "0~a\nb\x1b[2", "01~", "\x1b[3~", "\x1b[1;5C"} {
		got = append(got, d.feed([]byte(b))...)
	}
	want := []shellKey{{kind: keyRune, r: 'é'}, {kind: keyUp}, {kind: keyPasteStart}, {kind: keyRune, r: 'a'},
		{kind: keyEnter}, {kind: keyRune, r: 'b'}, {kind: keyPasteEnd}, {kind: keyDelete}, {kind: keyRight}}
	if !slices.Equal(got, want) {
		t.Fatalf("keys %v, want %v", got, want)
	}
}

// Editing: Backspace, Left and Home edit the line; Up recalls the last line; Ctrl+C drops the line
// and a pending chunk.
func TestTheShellEditsItsLine(t *testing.T) {
	r := newShellRig(t, "")
	r.start()
	r.write("1+2\x7f3\x1b[D\x1b[D\x1b[H(\x1b[F)\r") // 1+3 with ( at the start, ) at the end
	r.runPost()
	r.until("4", func() bool { return strings.Contains(r.output(), "\r\n4\r\n") })
	r.write("\x1b[A\r") // the last line again
	r.runPost()
	r.until("4 twice", func() bool { return strings.Count(r.output(), "\r\n4\r\n") == 2 })
	r.write("for i=1,2 do\r")
	r.runPost()
	r.until("the continuation", func() bool { return strings.HasSuffix(r.output(), shellMore) })
	r.write("\x03")
	r.until("the interrupt", func() bool { return strings.HasSuffix(r.output(), "^C\r\n\r\x1b[K> ") })
}

// End to end in the program: :lua opens a shell tab with the keyboard; typing a chunk shows its
// value; :lua <chunk> notifies its values and keeps its globals; Ctrl+D closes the tab.
func TestTheLuaShellInTheTerminal(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	if err := r.luaRun("lua"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "AutoDoc's engine")
	r.s.WaitFor(t, "the shell's keyboard", func(string) bool { return r.focusedOn("terminalView") })
	if !slices.Contains(r.widgetNames(), "terminal/lua") {
		t.Fatalf("the shell's tab is not a target: %v", r.widgetNames())
	}
	for _, ch := range "6*7" {
		r.keys(t, key(ch))
	}
	r.keys(t, enter())
	r.s.WaitForText(t, "42")
	if err := r.luaRun("lua q = 5"); err != nil {
		t.Fatal(err)
	}
	if err := r.luaRun("lua q * 2"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the one-shot's notice", func(string) bool { return r.noticed("10") })
	if err := r.luaRun("lua for i=1,2 do"); err == nil || !strings.Contains(err.Error(), "not finished") {
		t.Errorf(":lua with an unfinished chunk: %v", err)
	}
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'd', Mods: tuicore.ModCtrl})
	r.s.WaitFor(t, "the tab closed", func(string) bool { return !slices.Contains(r.widgetNames(), "terminal/lua") })
}

// New Lua shell on a tab's menu adds a Lua shell tab, lua then lua 2, beside the shell; a program
// with no Lua shell's starter says so instead.
func TestALuaShellFromATabsMenu(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	if err := r.luaRun("terminal"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "a shell", func(string) bool { return len(r.shellTabs()) == 1 })
	newLua := func() {
		rows := onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows(r.shellTabs()[0].ID) })
		row, ok := menuRow(rows, "terminal.lua")
		if !ok {
			t.Fatalf("no New Lua shell in %v", rowIDs(rows))
		}
		onLoop(r, func() bool { row.Action.(widget.TerminalTabsMenuAction).Run(r.shellTabs()[0].ID); return true })
	}
	labels := func() []string {
		var out []string
		for _, tab := range r.shellTabs() {
			out = append(out, tab.Label)
		}
		return out
	}
	newLua()
	r.s.WaitFor(t, "a Lua shell", func(string) bool { return slices.Equal(labels(), []string{"shell", "lua"}) })
	r.s.WaitForText(t, "AutoDoc's engine")
	newLua()
	r.s.WaitFor(t, "a second", func(string) bool { return slices.Equal(labels(), []string{"shell", "lua", "lua 2"}) })

	onLoop(r, func() bool { r.h.terminals.startLua = nil; r.h.terminals.newLuaShell(); return true })
	r.s.WaitFor(t, "no starter said", func(string) bool { return r.noticed(errNoShell.Error()) })
	if len(labels()) != 3 {
		t.Fatalf("a tab was added without a starter: %v", labels())
	}
}
