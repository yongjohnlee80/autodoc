//go:build unix

package crew

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// tui/agents' fake agent server, for the crew mail's cells that start a Codex agent: a copy, naming its
// original (a POSIX shell plays the server).

// A copy of tui/agents' fakeServer (agentserver_own_test.go).
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

// A copy of tui/agents' endsWithTheTest (agentserver_own_test.go).
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

// A copy of tui/agents' liveProcessesNaming (agentserver_own_test.go).
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

// A copy of tui/agents' eventually (agentserver_own_test.go).
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

// A copy of tui/agents' processesNaming (agentserver_own_test.go).
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
