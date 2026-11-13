//go:build unix

package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// TestTheServerIsStartedWithTheCLIsEnvironment, end to end through runAgent: a crew Codex agent's
// server runs the shell commands of its agent, so it is started with what its CLI is: every
// variable the CLI's environment holds (the daemon, the agent's PATH, the editor socket and the
// agent's token there, the session's context, the setup's own), and none of the window's spawner
// identity (R4). None of them is on the CLI's command line or the server's, which
// /proc/<pid>/cmdline shows every user of the machine: the CLI is /bin/sh -c <command>.
func TestTheServerIsStartedWithTheCLIsEnvironment(t *testing.T) {
	tuneEnv(t, "AUTO_AGENTS_MAILBOX_ID=agent:the-window:1") // the window's spawner identity
	a, c, _ := crewAgents(t, crewKinds)
	a.EditorPath = func() string { return "/run/the-editor.sock" }
	p, _ := a.Profile("codex-one")
	const secret = "tok-setup-9a1b"
	var socket, envFile string
	seamsOf(a).agentSetupRun = func(_, dir string, _ []string) (crew.Setup, error) {
		socket, envFile = filepath.Join(dir, crew.CodexServerSocket), filepath.Join(dir, "server.env")
		endsWithTheTest(t, socket)
		server := []string{"sh", "-c", `env > "$2"; touch "$1"; while :; do sleep 1; done`, "sh", socket, envFile}
		return crew.Setup{Server: server, ServerSocket: socket, MailVia: crew.MailViaMCP, Env: map[string]string{"SVC_TOKEN": secret}}, nil
	}
	instance, err := a.StartInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	awaitStart(t, c, instance)
	cfg := c.panel.configs[instance.State().Tab]
	var spawned pty.Cmd // the CLI, as runAgent gives it
	seamsOf(a).agentSpawn = func(c pty.Cmd) (terminal.TerminalProcess, error) { spawned = c; return newFakeTerminal(), nil }
	proc, err := cfg.Start(pty.Cmd{Path: cfg.Command, Args: cfg.Args, Dir: cfg.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proc.Close() }()
	b, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	server := map[string]string{}
	for _, kv := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		k, v, _ := strings.Cut(kv, "=")
		server[k] = v
	}
	cli := environMap(spawned.Env)
	for _, k := range []string{crew.EnvContext, "AUTODOC_SOCKET", "AUTODOC_EDITOR", "AUTODOC_EDITOR_TOKEN", "PATH", "SVC_TOKEN"} {
		if cli[k] == "" {
			t.Fatalf("fixture: the CLI's environment has no %s: %q", k, spawned.Env)
		}
	}
	for k, v := range cli {
		if shellSets[k] {
			continue // the server's shell sets its own, as the CLI's /bin/sh does
		}
		if server[k] != v {
			t.Errorf("the server's %s is %q, the CLI's %q", k, server[k], v)
		}
	}
	for _, env := range []map[string]string{cli, server} {
		if v, ok := env["AUTO_AGENTS_MAILBOX_ID"]; ok {
			t.Errorf("the CLI or its server has the window's spawner identity: AUTO_AGENTS_MAILBOX_ID=%s", v)
		}
	}
	argv := slices.Concat([]string{spawned.Path}, spawned.Args, instance.State().Setup.Server)
	for _, k := range []string{"AUTODOC_EDITOR_TOKEN", crew.EnvContext, "SVC_TOKEN"} {
		for _, arg := range argv {
			if strings.Contains(arg, cli[k]) {
				t.Errorf("%s's value is on a command line: %q", k, arg)
			}
		}
	}
	if spawned.Path != "/bin/sh" || len(spawned.Args) < 2 || spawned.Args[0] != "-c" {
		t.Errorf("the CLI is %q %q, want /bin/sh -c <command>", spawned.Path, spawned.Args)
	}
}

// TestTheWindowStopsTheServerWhenItsCLIFailsToStart, end to end through runAgent: a crew Codex
// agent whose setup has a server has it started before its CLI, and stopped when the CLI fails to
// start, nothing of it left (the mutant: the server left running).
func TestTheWindowStopsTheServerWhenItsCLIFailsToStart(t *testing.T) {
	a, c, _ := crewAgents(t, crewKinds)
	p, _ := a.Profile("codex-one")
	var socket string
	seamsOf(a).agentSetupRun = func(_, dir string, _ []string) (crew.Setup, error) {
		socket = filepath.Join(dir, crew.CodexServerSocket)
		return crew.Setup{Server: fakeServer(t, socket, true), ServerSocket: socket, MailVia: crew.MailViaMCP}, nil
	}
	instance, err := a.StartInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	awaitStart(t, c, instance)
	cfg := c.panel.configs[instance.State().Tab]
	seamsOf(a).agentSpawn = func(pty.Cmd) (terminal.TerminalProcess, error) { return nil, errors.New("no such program") }
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

// TestACellsServersEndWithIt: a server a cell starts and never stops, as a cell through runAgent
// leaves one (its CLI never ends), is gone once the cell is, with its whole process group: a child
// that doesn't name the socket too (the mutants: endsWithTheTest's kill removed; the leader killed
// alone, its group left).
func TestACellsServersEndWithIt(t *testing.T) {
	groupEnded := func(name string, pgid int) {
		t.Helper()
		if pgid <= 1 || pgid == syscall.Getpgrp() {
			t.Fatalf("%s: no server's group (%d) to look for", name, pgid)
		}
		if !eventually(t, func() bool { return syscall.Kill(-pgid, 0) != nil }) {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			t.Fatalf("%s: the server's group %d outlives its cell", name, pgid)
		}
	}

	var pgid, child int
	t.Run("a server never stopped, its child naming no socket", func(t *testing.T) {
		dir, socket := serverDir(t)
		endsWithTheTest(t, socket)
		childFile := filepath.Join(dir, "child.pid")
		s, err := agents.StartAgentServer([]string{"sh", "-c", `touch "$1"; sleep 300 & echo $! > "$2"; while :; do sleep 1; done`, "sh", socket, childFile}, socket, dir, os.Environ(), defaultSeams.serverReady)
		if err != nil {
			t.Fatal(err)
		}
		pgid = s.Cmd.Process.Pid
		if !slices.Contains(liveProcessesNaming(socket), strconv.Itoa(pgid)) {
			t.Fatalf("fixture: the server %d doesn't name %s", pgid, socket)
		}
		// the child is live in the server's group, exec'd (its command line names no socket, so only
		// the group kill reaches it) before the subtest ends: its pid is published once it is forked
		var g int
		if !eventually(t, func() bool {
			b, _ := os.ReadFile(childFile)
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			var err error
			g, err = syscall.Getpgid(child)
			return child > 1 && err == nil && g == pgid && !slices.Contains(liveProcessesNaming(socket), strconv.Itoa(child))
		}) {
			t.Fatalf("fixture: the server's child %d (group %d) isn't live in the server's group %d, its command line naming no socket", child, g, pgid)
		}
	})
	groupEnded("a server never stopped", pgid)
	if !eventually(t, func() bool { return !alive(child) }) {
		t.Fatalf("the server's child %d outlives its cell", child)
	}

	var socket string
	t.Run("a crew Codex agent's server, its CLI never ending", func(t *testing.T) {
		a, c, _ := crewAgents(t, crewKinds)
		p, _ := a.Profile("codex-one")
		instance, err := a.StartInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		awaitStart(t, c, instance)
		if _, _, err := startedEnv(a, c, instance); err != nil { // a spawn that starts nothing: the server is never stopped
			t.Fatal(err)
		}
		socket = instance.State().Setup.ServerSocket
		pids := liveProcessesNaming(socket)
		if len(pids) != 1 {
			t.Fatalf("fixture: %d processes name the agent's socket %q, want its server", len(pids), socket)
		}
		pgid, _ = strconv.Atoi(pids[0])
	})
	groupEnded("a crew Codex agent's server", pgid)
	if pids := liveProcessesNaming(socket); len(pids) > 0 {
		t.Fatalf("the agent's server outlives its cell: %v", pids)
	}
}
