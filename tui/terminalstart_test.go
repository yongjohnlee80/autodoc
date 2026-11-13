package tui

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// heldShell is a shell tab's program spawned as the test says: each spawn waits until released,
// says it is out, and answers a pipe program whose Close is counted.
type heldShell struct {
	out     chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	procs   []*pipeShell
}

func newHeldShell() *heldShell {
	return &heldShell{out: make(chan struct{}, 4), release: make(chan struct{})}
}

func (s *heldShell) free() { s.once.Do(func() { close(s.release) }) }

func (s *heldShell) start(pty.Cmd) (terminal.TerminalProcess, error) {
	s.out <- struct{}{}
	<-s.release
	r, w := io.Pipe()
	p := &pipeShell{r: r, w: w, done: make(chan struct{})}
	s.mu.Lock()
	s.procs = append(s.procs, p)
	s.mu.Unlock()
	return p, nil
}

func (s *heldShell) spawnOut(t *testing.T) {
	t.Helper()
	select {
	case <-s.out:
	case <-time.After(daemonWait):
		t.Fatal("the shell's spawn never began")
	}
}

// closedOnce waits for the one program spawned to be closed, once.
func (s *heldShell) closedOnce(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(daemonWait); ; time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		var n int32 = -1
		if len(s.procs) == 1 {
			n = s.procs[0].closes.Load()
		}
		s.mu.Unlock()
		if n == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the late shell was never closed (closes %d)", n)
		}
	}
}

// pipeShell is a program that writes nothing until closed, its Close counted.
type pipeShell struct {
	r      *io.PipeReader
	w      *io.PipeWriter
	closes atomic.Int32
	done   chan struct{}
	once   sync.Once
}

func (p *pipeShell) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeShell) Write(b []byte) (int, error) { return len(b), nil }
func (p *pipeShell) Resize(int, int) error       { return nil }
func (p *pipeShell) Close() error {
	p.closes.Add(1)
	p.once.Do(func() { p.w.Close(); close(p.done) })
	return nil
}
func (p *pipeShell) Wait() (int, error) { <-p.done; return 0, nil }

// shellTUI is a window whose shells the test's held spawn starts, connected and ready.
func shellTUI(t *testing.T, s *heldShell) *running {
	t.Helper()
	tune(t).shellSpawn = s.start
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	t.Cleanup(s.free) // registered last, so it runs first: before the window and the daemon stop
	r.ready(t)
	return r
}

// TestAShellTabOpensAtOnceWhileItsShellStarts: opening the terminal panel draws its first tab at
// once, saying it is starting, while the shell's spawn is still out; the window goes on taking
// keys meanwhile (the panel hides and shows again), and the shell runs once its spawn returns.
func TestAShellTabOpensAtOnceWhileItsShellStarts(t *testing.T) {
	t.Parallel()
	s := newHeldShell()
	r := shellTUI(t, s)
	r.leader(t, '`') // the panel, and its first shell
	s.spawnOut(t)
	r.s.WaitForText(t, "[starting…]")
	r.leader(t, '`') // a key the window takes while the spawn is out: the panel hides
	r.s.WaitFor(t, "the panel hidden while the shell starts", func(string) bool { return !r.termShown() })
	r.leader(t, '`')
	r.s.WaitFor(t, "the panel shown again", func(string) bool { return r.termShown() })
	if n := r.shellsRunning(); n != 0 {
		t.Fatalf("%d shells running before the spawn returned", n)
	}
	s.free()
	r.waitWithin(t, "the shell running", daemonWait, func() bool { return r.shellsRunning() == 1 })
}

// TestAShellTabClosedWhileStartingLeavesNoShell: a tab closed while its shell's spawn is out is
// gone at once, and the shell the spawn then returns is closed, never run.
func TestAShellTabClosedWhileStartingLeavesNoShell(t *testing.T) {
	t.Parallel()
	s := newHeldShell()
	r := shellTUI(t, s)
	r.leader(t, '`')
	s.spawnOut(t)
	if err := onLoop(r, func() error {
		panel, err := r.h.terminals.panel()
		if err != nil {
			return err
		}
		_, err = panel.Remove(panel.Active())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.free()
	s.closedOnce(t)
	if n := r.shellsRunning(); n != 0 {
		t.Fatalf("%d shells running after their tab was closed while starting", n)
	}
}

// TestALuaShellStartsOnTheLoop: a Lua shell's tab starts on the UI loop, its shell made of loop
// state (its banner in the UI's language), so a language applied on the loop while it opens
// races nothing. Started off the loop, the shell read the language as the loop changed it (-race).
func TestALuaShellStartsOnTheLoop(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool {
		r.h.terminals.newLuaShell()
		for i := range 400 {
			lang := "en"
			if i%2 == 0 {
				lang = "es"
			}
			r.h.p.App().ApplyLanguage(lang)
		}
		return true
	})
	r.waitWithin(t, "the Lua shell running", daemonWait, func() bool {
		return onLoop(r, func() bool {
			panel, err := r.h.terminals.panel()
			if err != nil {
				return false
			}
			for _, tab := range panel.Tabs() {
				if tab.Running {
					return true
				}
			}
			return false
		})
	})
}
