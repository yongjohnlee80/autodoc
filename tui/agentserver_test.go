//go:build unix

package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// fakeServer is a server's argv that makes socket (a file stands in) and runs until stopped, its
// command line naming socket for as long as it runs, as a real one's does (`codex app-server
// --listen unix://<socket>`); "never" listens never makes it. The shell stays: an `exec sleep`
// would put sleep's command line in its place, which names no socket, so the server would be taken
// for another process by whatever looked after the exec (a flake, 1 in 5 under -race, 2026-11-01).
func fakeServer(socket string, listens bool) []string {
	script := "while :; do sleep 1; done"
	if listens {
		script = `touch "$1"; while :; do sleep 1; done`
	}
	return []string{"sh", "-c", script, "sh", socket}
}

// namesItsSocket fails the cell unless pid's command line still names socket a while after its
// start: the fixture's premise, which stopStaleServer's identification rests on.
func namesItsSocket(t *testing.T, pid int, socket string) {
	t.Helper()
	time.Sleep(200 * time.Millisecond) // past a shell's exec, which would have replaced the command line
	if !slices.Contains(processesNaming(socket), strconv.Itoa(pid)) {
		t.Fatalf("the fake server %d no longer names %s: the cell would test nothing", pid, socket)
	}
}

// processesNaming are the live processes whose command line names s (/proc, as stopStaleServer reads).
func processesNaming(s string) []string {
	var out []string
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err == nil && strings.Contains(string(b), s) {
			if st, err := os.ReadFile("/proc/" + e.Name() + "/stat"); err == nil && !strings.Contains(string(st), ") Z ") {
				out = append(out, e.Name())
			}
		}
	}
	return out
}

// alive reports whether pid is a process (a zombie aside: its parent has reaped it).
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// eventually waits up to 5 s for ok.
func eventually(t *testing.T, ok func() bool) bool {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return true
		}
	}
	return ok()
}

func serverDir(t *testing.T) (dir, socket string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".autodoc-crew"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, filepath.Join(dir, crew.CodexServerSocket)
}

// TestAnAgentServerRunsInItsOwnGroupUntilStopped: a started server is waited for until its socket
// is there, runs in a process group of its own (never the window's), has its pid in the folder's
// pid file, and stopping it ends its group and removes the file (the mutants: no group of its own;
// the pid file not written).
func TestAnAgentServerRunsInItsOwnGroupUntilStopped(t *testing.T) {
	dir, socket := serverDir(t)
	s, err := startAgentServer(fakeServer(socket, true), socket, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	pid := s.cmd.Process.Pid
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid || pgid == syscall.Getpgrp() {
		t.Fatalf("the server's group %d (%v), want its own (%d), not the window's (%d)", pgid, err, pid, syscall.Getpgrp())
	}
	if b, err := os.ReadFile(filepath.Join(dir, serverPidFile)); err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(pid) {
		t.Fatalf("the pid file %q %v, want %d", b, err, pid)
	}
	s.stop()
	if !eventually(t, func() bool { return !alive(pid) }) {
		t.Fatal("the stopped server still runs")
	}
	if _, err := os.Stat(filepath.Join(dir, serverPidFile)); !os.IsNotExist(err) {
		t.Fatalf("the pid file after the stop: %v, want it gone", err)
	}
	s.stop() // again: nothing more
}

// TestStoppingAServerEndsItsWholeGroup: a server whose child outlives the SIGTERM (it ignores it,
// as a slow MCP child may) leaves no process once stopped: the group is killed after its leader
// ends (the mutant: the stop ends at the leader's end, and the child is an orphan).
func TestStoppingAServerEndsItsWholeGroup(t *testing.T) {
	dir, socket := serverDir(t)
	child := `sh -c 'trap "" TERM; touch "$1"; while :; do sleep 1; done' child "$1" & exec sleep 300`
	s, err := startAgentServer([]string{"sh", "-c", child, "sh", socket}, socket, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	pgid := s.cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	s.stop()
	if !eventually(t, func() bool { return len(processesNaming(socket)) == 0 }) {
		t.Fatalf("the server's child outlives its stop: %v", processesNaming(socket))
	}
}

// TestAServerThatNeverListensIsStoppedAndRefused: a server whose socket doesn't appear within
// serverReadyWait is stopped, its pid file removed, and the start refused (the mutant: left running).
func TestAServerThatNeverListensIsStoppedAndRefused(t *testing.T) {
	saved := serverReadyWait
	serverReadyWait = 300 * time.Millisecond
	t.Cleanup(func() { serverReadyWait = saved })
	dir, socket := serverDir(t)
	s, err := startAgentServer(fakeServer(socket, false), socket, dir, os.Environ())
	if err == nil || s != nil || !strings.Contains(err.Error(), "didn't listen") {
		t.Fatalf("a server that never listens: %v %v, want refused", s, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, serverPidFile))
	if len(b) != 0 {
		t.Fatalf("its pid file is left: %q", b)
	}
	if pids := processesNaming(socket); len(pids) > 0 {
		t.Fatalf("its process is left: %v", pids)
	}
}

// TestAStaleServerIsStoppedFirst: a server a window that died left in the folder (its pid file,
// its command line naming the folder's socket) is stopped before the next one starts; a pid the
// file names that is another process now (its command line doesn't name the socket) is left alone
// (the mutants: the stale one kept; any pid killed).
func TestAStaleServerIsStoppedFirst(t *testing.T) {
	dir, socket := serverDir(t)
	start := func(argv []string) *exec.Cmd {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _, _ = cmd.Process.Wait() })
		return cmd
	}
	stale := start(fakeServer(socket, false))
	namesItsSocket(t, stale.Process.Pid, socket)
	var reaped sync.WaitGroup
	reaped.Add(1)
	go func() { _, _ = stale.Process.Wait(); reaped.Done() }()
	pidFile := filepath.Join(dir, serverPidFile)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stale.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := startAgentServer(fakeServer(socket, true), socket, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer s.stop()
	done := make(chan struct{})
	go func() { reaped.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stale server still runs after the next start")
	}

	other := start([]string{"sleep", "300"}) // a pid the file names that is no server of this folder
	s.stop()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(other.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := startAgentServer(fakeServer(socket, true), socket, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.stop()
	if !alive(other.Process.Pid) {
		t.Fatal("another process the pid file named was stopped")
	}
}

// fakeTerminal is an agent CLI's process: Wait returns once ended, Close records the terminal closed.
type fakeTerminal struct {
	widget.TerminalProcess
	ended  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newFakeTerminal() *fakeTerminal {
	return &fakeTerminal{ended: make(chan struct{}), closed: make(chan struct{})}
}
func (f *fakeTerminal) Close() error          { f.once.Do(func() { close(f.closed) }); return nil }
func (f *fakeTerminal) Wait() (int, error)    { <-f.ended; return 0, nil }
func (f *fakeTerminal) Resize(_, _ int) error { return nil }

// TestTheServerAndItsCLIEndTogether: bound to its CLI, the server is stopped when the CLI ends, and
// its own end closes the CLI's terminal (so the CLI, and its session, end): no TUI is left on a
// dead --remote, and no server outlives its CLI (the mutants: either side's end not passed on).
func TestTheServerAndItsCLIEndTogether(t *testing.T) {
	dir, socket := serverDir(t)
	s, err := startAgentServer(fakeServer(socket, true), socket, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	cli := newFakeTerminal()
	proc := bindServer(cli, s)
	close(cli.ended)
	_, _ = proc.Wait()
	if !eventually(t, func() bool { return !alive(s.cmd.Process.Pid) }) {
		t.Fatal("the CLI ended and its server still runs")
	}

	dir, socket = serverDir(t)
	s, err = startAgentServer(fakeServer(socket, true), socket, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	cli = newFakeTerminal()
	_ = bindServer(cli, s)
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL) // the server dies first
	select {
	case <-cli.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the server died and its CLI's terminal stays open")
	}
	s.stop()
}

// TestTheWindowStopsTheServerWhenItsCLIFailsToStart, end to end through runAgent: a crew Codex
// agent whose setup has a server has it started before its CLI, and stopped when the CLI fails to
// start, nothing of it left (the mutant: the server left running).
func TestTheWindowStopsTheServerWhenItsCLIFailsToStart(t *testing.T) {
	a, c, _ := crewAgents(t, crewKinds)
	p, _ := a.profile("codex-one")
	var socket string
	a.setupRun = func(_, dir string, _ []string) (crew.Setup, error) {
		socket = filepath.Join(dir, crew.CodexServerSocket)
		return crew.Setup{Server: fakeServer(socket, true), ServerSocket: socket, MailVia: crew.MailViaMCP}, nil
	}
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	awaitStart(t, c, instance)
	cfg := c.panel.configs[instance.tab]
	a.spawn = func(pty.Cmd) (widget.TerminalProcess, error) { return nil, errors.New("no such program") }
	if _, err := cfg.Start(pty.Cmd{Path: cfg.Command, Args: cfg.Args, Dir: cfg.Dir}); err == nil {
		t.Fatal("a CLI that failed to start: started")
	}
	if !eventually(t, func() bool { return len(processesNaming(socket)) == 0 }) {
		t.Fatalf("the CLI didn't start and its server still runs: %v", processesNaming(socket))
	}
	if _, err := os.Stat(filepath.Join(cfg.Dir, serverPidFile)); !os.IsNotExist(err) {
		t.Fatalf("its pid file: %v, want it gone", err)
	}
}
