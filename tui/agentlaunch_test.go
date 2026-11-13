//go:build linux || darwin

package tui

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/agents"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// TestAnAgentRetriedWhileStartedAgainReadsNoLiveState: an agent tab whose start failed is retried
// with Enter (the terminal's own retry, its starter off the loop), and while that spawn is out the
// window starts the agent again, writing the instance on the loop. The starter reads only the
// launch the loop published for it, never the instance, so a -race run reports nothing: before the
// launch, its reads of the instance before and after the spawn both raced that write. The start the
// window asked for joins the retry's, and the agent runs.
//
// Nothing orders the starter with the loop: it only sleeps while out, and the test learns it is out
// from the screen. A channel, a lock or an atomic would order them, and so would any I/O (a race
// build orders every syscall read after every syscall write, syscall's ioSync), as a fork, a pipe or
// a file would. Keep the hold sleep-only: each of those, tried, left the old code green.
func TestAnAgentRetriedWhileStartedAgainReadsNoLiveState(t *testing.T) {
	t.Parallel()
	agent, _ := fakeAgent(t)
	var spawns atomic.Int32 // read only once the retry's start has landed
	pr, pw := io.Pipe()
	held := &pipeShell{r: pr, w: pw, done: make(chan struct{})} // the retry's program, made before
	t.Cleanup(func() { held.Close() })
	s := seamsWithEnv(t, nil)
	s.setAgentSpawn(func(c pty.Cmd) (terminal.TerminalProcess, error) {
		switch spawns.Add(1) {
		case 1:
			return nil, errors.New("no such program")
		case 2:
			time.Sleep(2 * time.Second) // out, ordered with nothing
			return held, nil
		}
		return pty.Start(c)
	})
	profiles := []agents.AgentProfile{{Name: "heron", Command: agents.ShellQuote(agent), Codename: "heron"}}
	_, r, _ := runAgentTUIWith(t, t.TempDir(), profiles, "heron", nil, Options{seams: s})
	r.leader(t, 'g')
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	starting := 0
	r.s.WaitFor(t, "the failed start shown", func(screen string) bool {
		starting = strings.Count(screen, "[starting")
		return strings.Contains(screen, "no such program") && starting > 0
	})
	r.waitWithin(t, "the failed start landed", daemonWait, func() bool {
		return onLoop(r, func() bool { in := r.h.agents.Snapshot().Instances["heron"]; return !in.Starting && !in.Running })
	})
	r.keys(t, tuicore.KeyEvent{Code: tuicore.KeyEnter}) // the terminal retries the failed start
	r.s.WaitFor(t, "the retry starting", func(screen string) bool { return strings.Count(screen, "[starting") > starting })
	// the window's start, on the loop, while the retry's spawn is out: it sets the instance up again
	why := onLoop(r, func() string {
		in := r.h.agents.Snapshot().Instances["heron"]
		if in.Starting || in.Running {
			return fmt.Sprintf("starting %v running %v before the window's start: it would set nothing up", in.Starting, in.Running)
		}
		p, _ := r.h.agents.Profile("heron")
		if _, err := r.h.agents.StartInstance(p); err != nil {
			return err.Error()
		}
		if !r.h.agents.Snapshot().Instances["heron"].Starting {
			return "the window's start did not start the instance"
		}
		return ""
	})
	if why != "" {
		t.Fatal(why)
	}
	r.waitWithin(t, "the agent running", daemonWait, func() bool { return r.agentRunningNow() })
	if n := spawns.Load(); n != 2 {
		t.Fatalf("%d spawns, want 2: the window's start joins the retry's in flight", n)
	}
}
