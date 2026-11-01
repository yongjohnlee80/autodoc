//go:build unix

package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// A crew agent's server (its setup's Server: a Codex agent's own app-server, the crew MCP server on
// its command line) is the window's to run, with no orphan left: started before the agent's CLI, in
// a process group of its own, its pid in the agent folder's serverPidFile; the CLI started once its
// socket is there; stopped when the CLI ends or fails to start; and when it ends first, the CLI's
// terminal is closed, so the CLI ends and its session with it. A window that died leaves its
// server: the next start in that folder stops it first, by its pid file, when that process's
// command line still names the folder's socket.

// serverPidFile is the agent server's pid, in the agent folder (0700). Never in its crew folder
// (crew.ContextDir): that holds the session contexts alone, one file per session, and a file of
// anything else there is read as one more context, so `autodoc crew` and `mail` in the folder
// refused with "several session contexts".
const serverPidFile = ".autodoc-server.pid"

// legacyServerPidFile is where builds before it kept serverPidFile, in the crew folder: a start
// stops the server it names, as it does serverPidFile's, and removes it, so a folder those builds
// wrote reads its one context again.
const legacyServerPidFile = ".autodoc-crew/server.pid"

// serverReadyWait bounds the wait for a started server's socket; a server not listening by then is
// stopped, and the CLI isn't started. A cell gives its own.
var serverReadyWait = 10 * time.Second

const (
	// serverStopWait is how long a stopped server's group has to end after SIGTERM before SIGKILL.
	serverStopWait = 3 * time.Second
	// serverPoll is how often a server's socket, or a stale server's end, is looked for.
	serverPoll = 50 * time.Millisecond
)

// agentServer is one running agent server.
type agentServer struct {
	cmd     *exec.Cmd
	pidFile string
	done    chan struct{} // closed once the process has ended
	once    sync.Once
}

// startAgentServer starts argv in dir with environ, a stale server of the folder stopped first, and
// waits for socket; on any failure nothing is left running.
func startAgentServer(argv []string, socket, dir string, environ []string) (*agentServer, error) {
	if len(argv) == 0 || socket == "" {
		return nil, errors.New("crew setup gave a server with no command or no socket")
	}
	pidFile := filepath.Join(dir, serverPidFile)
	stopStaleServer(filepath.Join(dir, legacyServerPidFile), socket)
	stopStaleServer(pidFile, socket)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, environ
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // its own group: stopped whole, never the window's
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("the agent's server (%s): %w", argv[0], err)
	}
	s := &agentServer{cmd: cmd, pidFile: pidFile, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(s.done) }()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		s.stop()
		return nil, fmt.Errorf("the agent's server's pid file: %w", err)
	}
	deadline := time.Now().Add(serverReadyWait)
	for {
		if _, err := os.Stat(socket); err == nil {
			return s, nil
		}
		select {
		case <-s.done:
			s.stop()
			return nil, fmt.Errorf("the agent's server (%s) ended before it listened on %s", argv[0], socket)
		// NOTE: can't announce: a server says nothing when it listens, so its socket is looked for
		// every serverPoll, until serverReadyWait (a named deadline).
		case <-time.After(serverPoll):
		}
		if time.Now().After(deadline) {
			s.stop()
			return nil, fmt.Errorf("the agent's server (%s) didn't listen on %s within %s", argv[0], socket, serverReadyWait)
		}
	}
}

// stop ends the server's group (SIGTERM, then SIGKILL after serverStopWait, and SIGKILL to what is
// left of the group once its leader has ended) and removes its pid file; it is safe to call again.
func (s *agentServer) stop() {
	s.once.Do(func() {
		pgid := s.cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case <-s.done:
		// NOTE: a grace period: a server that doesn't end on SIGTERM within serverStopWait is killed.
		case <-time.After(serverStopWait):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-s.done
		}
		// the leader's end isn't its group's: a child that outlived the SIGTERM is killed too
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = os.Remove(s.pidFile)
	})
}

// stopStaleServer stops the server a window that died left in the folder: the pid in pidFile, when
// that process's command line still names socket (a pid reused by anything else is left alone).
func stopStaleServer(pidFile, socket string) {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		_ = os.Remove(pidFile)
		return
	}
	cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err == nil && strings.Contains(string(cmdline), socket) {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		// NOTE: can't announce: another window's process ends without telling this one, so its end
		// is looked for every serverPoll, for the grace period serverStopWait, then it is killed.
		tick := time.NewTicker(serverPoll)
		for end := time.Now().Add(serverStopWait); time.Now().Before(end) && syscall.Kill(pid, 0) == nil; {
			<-tick.C
		}
		tick.Stop()
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = os.Remove(pidFile)
}

// serverBound is the agent CLI's process bound to its server: the CLI's end stops the server, and
// the server's end closes the CLI's terminal, so the CLI ends (and its session with it).
type serverBound struct {
	widget.TerminalProcess
	server *agentServer
}

func bindServer(proc widget.TerminalProcess, s *agentServer) widget.TerminalProcess {
	b := &serverBound{TerminalProcess: proc, server: s}
	go func() { <-s.done; _ = proc.Close() }()
	return b
}

func (b *serverBound) Wait() (int, error) {
	code, err := b.TerminalProcess.Wait()
	b.server.stop()
	return code, err
}
