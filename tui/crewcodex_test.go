package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// codexRuns is a stand-in for the agent's autodoc in runAfterSteps: each run's args are sent on
// calls, and answered by answer.
type codexRuns struct {
	calls   chan []string
	answer  error
	environ chan []string // each run's environment, when set
}

func (r *codexRuns) run(environ []string, args ...string) error {
	if r.environ != nil {
		r.environ <- environ
	}
	r.calls <- args
	return r.answer
}

// TestACodexAgentsStepLooksOnItsOwnServer: the registration step runs in the CLI's environment,
// its setup's included, so a Codex agent given its own app-server (crew.EnvCodexServer) looks for its
// thread there, not on the shared one (the mutant: the profile's environment alone, as before).
func TestACodexAgentsStepLooksOnItsOwnServer(t *testing.T) {
	a, c, _ := crewAgents(t, crewKinds)
	runs := &codexRuns{calls: make(chan []string, 4), environ: make(chan []string, 4)}
	seamsOf(a).agentCodexHere = runs.run
	seamsOf(a).agentSetupRun = func(string, string, []string) (crew.Setup, error) {
		return crew.Setup{Env: map[string]string{crew.EnvCodexServer: "/agent/codex.sock"},
			After: [][]string{{"crew", "endpoint", "--codex-here", "--since", crew.SetupSince, "--wait", "60s"}}}, nil
	}
	p, _ := a.Profile("codex-one")
	instance, err := a.StartInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := startedEnv(a, c, instance); err != nil {
		t.Fatal(err)
	}
	select {
	case environ := <-runs.environ:
		if !slices.Contains(environ, crew.EnvCodexServer+"=/agent/codex.sock") {
			t.Fatalf("the step's environment lacks the agent's own server: %q", environ)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no run of the agent's autodoc")
	}
}

// TestACrewCodexAgentIsRegisteredAsItStarts: a crew Codex agent's spawn has the window run its
// setup's step (crew.SetupFor) once, off the loop: its autodoc's `crew endpoint --codex-here
// --since <a moment before the spawn> --wait 60s`, which looks for the folder's thread itself and
// raises the session's attention when it can't (app's TestCodexWaitLoopsUntilFoundEndedOrDone).
// The window sets no timer of its own. Another CLI's spawn runs none of it.
func TestACrewCodexAgentIsRegisteredAsItStarts(t *testing.T) {
	t.Run("one waiting registration", func(t *testing.T) {
		if !agents.CanRunAgentServers {
			t.Skip("this system can't run an agent's own server: a Codex agent starts without one (the next subtest)")
		}
		a, c, _ := crewAgents(t, crewKinds)
		runs := &codexRuns{calls: make(chan []string, 4)}
		seamsOf(a).agentCodexHere = runs.run
		p, _ := a.Profile("codex-one")
		instance, err := a.StartInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		before := time.Now().Unix()
		if _, _, err := startedEnv(a, c, instance); err != nil {
			t.Fatal(err)
		}
		var args []string
		select {
		case args = <-runs.calls:
		case <-time.After(5 * time.Second):
			t.Fatal("no run of the agent's autodoc")
		}
		since, err := strconv.ParseInt(args[min(4, len(args)-1)], 10, 64)
		if len(args) != 7 || !slices.Equal(args[:4], []string{"crew", "endpoint", "--codex-here", "--since"}) ||
			!slices.Equal(args[5:], []string{"--wait", "60s"}) || err != nil || since > before || since < before-5 {
			t.Fatalf("the run: %q, want crew endpoint --codex-here --since <a moment before the spawn> --wait 60s", args)
		}
		select {
		case more := <-runs.calls:
			t.Fatalf("a second run %q, want one", more)
		case <-time.After(50 * time.Millisecond):
		}
		if len(c.afters) != 0 {
			t.Fatalf("the window set %d timers, want none: the step waits itself", len(c.afters))
		}
	})

	// a host that can't run an agent's own server (the window where there are no process groups):
	// the agent starts with no server of its own, off Codex's shared daemon (--no-daemon), and
	// with no registration step, since no server holds its thread; the user is told, once, that
	// crew mail can't wake it. Forced here on every OS: it is what Windows does.
	t.Run("on a host that can't run its server", func(t *testing.T) {
		a, c, _ := crewAgentsOn(t, crewKinds, true)
		runs := &codexRuns{calls: make(chan []string, 4)}
		seamsOf(a).agentCodexHere = runs.run
		p, _ := a.Profile("codex-one")
		instance, err := a.StartInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		cmd, spawned, err := startedCmd(a, c, instance)
		if err != nil || !spawned {
			t.Fatalf("the start: spawned %v, %v; want Codex started without a server of its own", spawned, err)
		}
		if !slices.Contains(cmd.Args, "--no-daemon") || slices.Contains(cmd.Args, "--remote") {
			t.Fatalf("its command %q: want --no-daemon, and no --remote to a server of its own", cmd.Args)
		}
		select {
		case args := <-runs.calls:
			t.Fatalf("a run %q: want no registration step, with no server to find its thread on", args)
		case <-time.After(50 * time.Millisecond):
		}
		said := 0
		for _, n := range c.notes {
			if strings.Contains(n, "crew mail can't wake it") {
				said++
			}
		}
		if said != 1 {
			t.Fatalf("the notices %q: want it said once that crew mail can't wake it", c.notes)
		}
	})

	t.Run("another CLI's spawn runs none", func(t *testing.T) {
		a, c, _ := crewAgents(t, crewKinds)
		runs := &codexRuns{calls: make(chan []string, 4)}
		seamsOf(a).agentCodexHere = runs.run
		p, _ := a.Profile("claude-one")
		instance, err := a.StartInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := startedEnv(a, c, instance); err != nil {
			t.Fatal(err)
		}
		select {
		case args := <-runs.calls:
			t.Fatalf("a Claude spawn ran %q", args)
		case <-time.After(50 * time.Millisecond):
		}
	})
}
