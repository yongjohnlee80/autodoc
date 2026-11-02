package tui

import (
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// A crew agent's after-step that waits for something itself (a Codex agent's `crew endpoint
// --codex-here --wait`, as its setup gives it) runs past its own wait before the window would kill
// it: at its deadline it raises the session's attention, which a kill at 15 s never let it do. A
// step that waits for nothing is bounded at 15 s.
func TestAnAfterStepRunsPastItsOwnWait(t *testing.T) {
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Program: "codex", Host: crew.HostWindow, Autodoc: "autodoc",
		AgentDir: t.TempDir(), DataDir: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	waits := 0
	for _, step := range s.After {
		for i, arg := range step {
			if arg != "--wait" || i+1 >= len(step) {
				continue
			}
			waits++
			own, err := time.ParseDuration(step[i+1])
			if err != nil {
				t.Fatalf("the step %v waits %q", step, step[i+1])
			}
			if bound := agentAutodocBound(step); bound <= own {
				t.Errorf("the step %v waits %v itself, and the window kills it at %v", step, own, bound)
			}
		}
	}
	if waits == 0 {
		t.Fatalf("a Codex agent's after-steps %v have no step that waits: the cell checks nothing", s.After)
	}
	for _, args := range [][]string{{"crew", "endpoint", "--codex-here"}, {"crew", "endpoint", "--wait", "soon"}} {
		if got := agentAutodocBound(args); got != 15*time.Second {
			t.Errorf("%v is bounded at %v, want 15 s", args, got)
		}
	}
	if got := agentAutodocBound([]string{"crew", "endpoint", "--wait=2m"}); got != 15*time.Second+2*time.Minute {
		t.Errorf("--wait=2m is bounded at %v, want 2m15s", got)
	}
}
