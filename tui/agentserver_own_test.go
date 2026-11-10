//go:build unix

package tui

import (
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

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// fakeServer is a server's argv that makes socket (a file stands in) and runs until stopped, its
// command line naming socket for as long as it runs, as a real one's does (`codex app-server
// --listen unix://<socket>`); "never" listens never makes it. The shell stays: an `exec sleep`
// would put sleep's command line in its place, which names no socket, so the server would be taken
// for another process by whatever looked after the exec (a flake, 1 in 5 under -race, 2026-11-01).
// It ends with t (endsWithTheTest).
func fakeServer(t *testing.T, socket string, listens bool) []string {
	endsWithTheTest(t, socket)
	script := "while :; do sleep 1; done"
	if listens {
		script = `touch "$1"; while :; do sleep 1; done`
	}
	return []string{"sh", "-c", script, "sh", socket}
}

// endsWithTheTest kills, at t's cleanup, the process group of every live process whose command
// line names socket, and fails t if one is left. A cell's server started through runAgent is bound
// to a CLI that never ends there (a spawn that starts nothing, or a fake terminal never ended), so
// nothing else stops it: fake servers ran on for as long as the machine did. Only a process naming
// socket, which is in the cell's own temporary folder, is touched, so a reused pid never is; and
// never the test's own group.
func endsWithTheTest(t *testing.T, socket string) {
	t.Helper()
	t.Cleanup(func() {
		for _, p := range liveProcessesNaming(socket) {
			pid, _ := strconv.Atoi(p)
			if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 1 && pgid != syscall.Getpgrp() {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if !eventually(t, func() bool { return len(liveProcessesNaming(socket)) == 0 }) {
			t.Errorf("processes naming %s outlive the test: %v", socket, liveProcessesNaming(socket))
		}
	})
}

// liveProcessesNaming is processesNaming where there is a /proc, and ps's answer where there is
// none (macOS), so a cell's servers end with it there too.
func liveProcessesNaming(s string) []string {
	if _, err := os.Stat("/proc/self/cmdline"); err == nil {
		return processesNaming(s)
	}
	b, err := exec.Command("ps", "-A", "-o", "pid=,stat=,command=").Output()
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && !strings.HasPrefix(f[1], "Z") && strings.Contains(strings.Join(f[2:], " "), s) {
			out = append(out, f[0])
		}
	}
	return out
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
	s, err := startAgentServer(fakeServer(t, socket, true), socket, dir, os.Environ(), newFakeSeams().serverReady)
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
	s, err := startAgentServer([]string{"sh", "-c", child, "sh", socket}, socket, dir, os.Environ(), newFakeSeams().serverReady)
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
// its ready wait is stopped, its pid file removed, and the start refused (the mutant: left running).
func TestAServerThatNeverListensIsStoppedAndRefused(t *testing.T) {
	dir, socket := serverDir(t)
	s, err := startAgentServer(fakeServer(t, socket, false), socket, dir, os.Environ(), 300*time.Millisecond)
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
	stale := start(fakeServer(t, socket, false))
	namesItsSocket(t, stale.Process.Pid, socket)
	var reaped sync.WaitGroup
	reaped.Add(1)
	go func() { _, _ = stale.Process.Wait(); reaped.Done() }()
	pidFile := filepath.Join(dir, serverPidFile)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stale.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := startAgentServer(fakeServer(t, socket, true), socket, dir, os.Environ(), newFakeSeams().serverReady)
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
	s2, err := startAgentServer(fakeServer(t, socket, true), socket, dir, os.Environ(), newFakeSeams().serverReady)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.stop()
	if !alive(other.Process.Pid) {
		t.Fatal("another process the pid file named was stopped")
	}
}

// TestTheServersPidFileStaysOutOfTheContextsFolder: the server's pid file is in the agent folder,
// never in its crew folder, which holds the session contexts alone: a file of anything else there
// was read as a second context, and `autodoc crew` and `mail` in the folder refused with "several
// session contexts" (R4). A server a build before it left, its pid in the crew folder, is stopped
// at the next start and that file removed, so the folder reads its one context again.
func TestTheServersPidFileStaysOutOfTheContextsFolder(t *testing.T) {
	dir, socket := serverDir(t)
	contexts := filepath.Join(dir, crew.ContextDir)
	if err := os.WriteFile(filepath.Join(contexts, "s1"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := exec.Command("sh", "-c", "while :; do sleep 1; done", "sh", socket)
	stale.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := stale.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-stale.Process.Pid, syscall.SIGKILL); _, _ = stale.Process.Wait() })
	namesItsSocket(t, stale.Process.Pid, socket)
	var reaped sync.WaitGroup
	reaped.Add(1)
	go func() { _, _ = stale.Process.Wait(); reaped.Done() }()
	if err := os.WriteFile(filepath.Join(contexts, "server.pid"), []byte(strconv.Itoa(stale.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := startAgentServer(fakeServer(t, socket, true), socket, dir, os.Environ(), newFakeSeams().serverReady)
	if err != nil {
		t.Fatal(err)
	}
	defer s.stop()
	done := make(chan struct{})
	go func() { reaped.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the server a build before it left, its pid in the crew folder, still runs after the next start")
	}
	var names []string
	es, err := os.ReadDir(contexts)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"s1"}) {
		t.Fatalf("the crew folder holds %q, want its one session context alone", names)
	}
	if b, err := os.ReadFile(filepath.Join(dir, serverPidFile)); err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(s.cmd.Process.Pid) {
		t.Fatalf("the pid file in the agent folder: %q %v, want %d", b, err, s.cmd.Process.Pid)
	}
}

// TestAPlainAgentsVariablesAreInItsEnvironment: an agent outside a crew is started as /bin/sh -c
// <command> too, the window's variables (the editor socket and the agent's token there among them)
// in its environment, none on its command line, and the spawner's identity not in it.
func TestAPlainAgentsVariablesAreInItsEnvironment(t *testing.T) {
	c := newIdentityHost(crewKinds)
	c.ws = feature.Workspace{ID: 7, Name: "kb", Root: t.TempDir()}
	a := testAgents(c, t.TempDir(), "")
	a.seams.(*fakeSeams).env = procenv.Env(nil).With("AUTO_AGENTS_MAILBOX_ID=agent:the-window:1") // the window's spawner identity
	a.editorPath = func() string { return "/run/the-editor.sock" }
	p, _ := a.profile("claude-one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if instance.crewed {
		t.Fatal("fixture: the agent is crewed")
	}
	cfg := c.panel.configs[instance.tab]
	var spawned pty.Cmd
	a.spawn = func(c pty.Cmd) (terminal.TerminalProcess, error) { spawned = c; return newFakeTerminal(), nil }
	proc, err := cfg.Start(pty.Cmd{Path: cfg.Command, Args: cfg.Args, Dir: cfg.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proc.Close() }()
	env := environMap(spawned.Env)
	for _, k := range []string{"AUTODOC_SOCKET", "AUTODOC_EDITOR", "AUTODOC_EDITOR_TOKEN", "PATH"} {
		if env[k] == "" {
			t.Fatalf("the agent's environment has no %s: %q", k, spawned.Env)
		}
	}
	if _, ok := env["AUTO_AGENTS_MAILBOX_ID"]; ok {
		t.Error("the agent has the window's spawner identity")
	}
	for _, arg := range append([]string{spawned.Path}, spawned.Args...) {
		if strings.Contains(arg, env["AUTODOC_EDITOR_TOKEN"]) || strings.Contains(arg, "AUTODOC_SOCKET=") {
			t.Errorf("a variable is on the agent's command line: %q", arg)
		}
	}
	if spawned.Path != "/bin/sh" || len(spawned.Args) != 2 || spawned.Args[0] != "-c" {
		t.Errorf("the agent is %q %q, want /bin/sh -c <command>", spawned.Path, spawned.Args)
	}
}

// shellSets are the variables a POSIX shell sets itself at its start, whatever its environment held.
var shellSets = map[string]bool{"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true}

// environMap is environ (os.Environ's form) as a map, a later entry winning, as exec.Cmd takes it.
func environMap(environ []string) map[string]string {
	out := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// TestTheServerAndItsCLIEndTogether: bound to its CLI, the server is stopped when the CLI ends, and
// its own end closes the CLI's terminal (so the CLI, and its session, end): no TUI is left on a
// dead --remote, and no server outlives its CLI (the mutants: either side's end not passed on).
func TestTheServerAndItsCLIEndTogether(t *testing.T) {
	dir, socket := serverDir(t)
	s, err := startAgentServer(fakeServer(t, socket, true), socket, dir, os.Environ(), newFakeSeams().serverReady)
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
	s, err = startAgentServer(fakeServer(t, socket, true), socket, dir, os.Environ(), newFakeSeams().serverReady)
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
