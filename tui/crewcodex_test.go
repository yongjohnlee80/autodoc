package tui

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
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
	a.codexHere = runs.run
	a.setupRun = func(string, string, []string) (crew.Setup, error) {
		return crew.Setup{Env: map[string]string{crew.EnvCodexServer: "/agent/codex.sock"},
			After: [][]string{{"crew", "endpoint", "--codex-here", "--since", crew.SetupSince, "--wait", "60s"}}}, nil
	}
	p, _ := a.profile("codex-one")
	instance, err := a.startInstance(p)
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
		a, c, _ := crewAgents(t, crewKinds)
		runs := &codexRuns{calls: make(chan []string, 4)}
		a.codexHere = runs.run
		p, _ := a.profile("codex-one")
		instance, err := a.startInstance(p)
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

	t.Run("another CLI's spawn runs none", func(t *testing.T) {
		a, c, _ := crewAgents(t, crewKinds)
		runs := &codexRuns{calls: make(chan []string, 4)}
		a.codexHere = runs.run
		p, _ := a.profile("claude-one")
		instance, err := a.startInstance(p)
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
