package tui

import (
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// codexRuns is a stand-in for the agent's autodoc in registerCodex: each run's args are sent on
// calls, and answered by answer (nil, an error, ...) in turn; past the list, the last answer.
type codexRuns struct {
	calls  chan []string
	answer []error
	n      int
}

func (r *codexRuns) run(args ...string) error {
	i := min(r.n, len(r.answer)-1)
	r.n++
	r.calls <- args
	return r.answer[i]
}

// pump waits for the next run, then for what it posted, and runs that on the loop (the cell).
func pump(t *testing.T, c *identityHost, runs *codexRuns) []string {
	t.Helper()
	var args []string
	select {
	case args = <-runs.calls:
	case <-time.After(5 * time.Second):
		t.Fatal("no run of the agent's autodoc")
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		c.postMu.Lock()
		n := len(c.posts)
		c.postMu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run posted nothing to the loop")
		}
	}
	c.drainPosts()
	return args
}

// TestACrewCodexAgentIsRegisteredAsItStarts: a crew Codex agent's spawn has the window run its
// autodoc's `crew endpoint --codex-here --since <spawn>`, off the loop, and again on the loop's
// timer until the folder's thread is found; a session that ended stops it; one never found raises
// the session's attention, saying why. Another CLI's spawn runs none of it.
func TestACrewCodexAgentIsRegisteredAsItStarts(t *testing.T) {
	notFound := errors.New("no Codex thread for this folder on the app-server yet")
	start := func(t *testing.T, answer ...error) (*agents, *identityHost, *agentInstance, *codexRuns) {
		a, c, _ := crewAgents(t, crewKinds)
		a.pluginEnv = func() (string, error) { return `OPENCODE_CONFIG_CONTENT={}`, nil }
		runs := &codexRuns{calls: make(chan []string, 64), answer: answer}
		a.codexHere = runs.run
		p, _ := a.profile("codex-one")
		instance, err := a.startInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		return a, c, instance, runs
	}

	t.Run("found on the third look", func(t *testing.T) {
		a, c, instance, runs := start(t, notFound, notFound, nil)
		before := time.Now().Unix()
		if _, _, err := startedEnv(a, c, instance); err != nil {
			t.Fatal(err)
		}
		first := pump(t, c, runs)
		since, err := strconv.ParseInt(first[len(first)-1], 10, 64)
		if !slices.Equal(first[:4], []string{"crew", "endpoint", "--codex-here", "--since"}) || err != nil ||
			since > before || since < before-5 {
			t.Fatalf("the first run: %q, want crew endpoint --codex-here --since <a moment before the spawn>", first)
		}
		for range 2 {
			if len(c.afters) != 1 || c.delays[len(c.delays)-1] != codexRegisterEvery {
				t.Fatalf("after a miss: %d waits, last %v; want one of %v", len(c.afters), c.delays, codexRegisterEvery)
			}
			afters := c.afters
			c.afters = nil
			afters[0]()
			pump(t, c, runs)
		}
		if runs.n != 3 || len(c.afters) != 0 {
			t.Fatalf("found on the third look: %d runs, %d waits left", runs.n, len(c.afters))
		}
	})

	t.Run("a session that ended stops it", func(t *testing.T) {
		a, c, instance, runs := start(t, notFound)
		if _, _, err := startedEnv(a, c, instance); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(instance.context); err != nil { // as its exit removes it
			t.Fatal(err)
		}
		pump(t, c, runs)
		if len(c.afters) != 0 || runs.n != 1 {
			t.Fatalf("after the session ended: %d waits, %d runs", len(c.afters), runs.n)
		}
	})

	t.Run("never found raises the session's attention", func(t *testing.T) {
		a, c, instance, runs := start(t, notFound)
		if _, _, err := startedEnv(a, c, instance); err != nil {
			t.Fatal(err)
		}
		pump(t, c, runs)
		for len(c.afters) > 0 {
			afters := c.afters
			c.afters = nil
			afters[0]()
			pump(t, c, runs)
		}
		var last []string
		select {
		case last = <-runs.calls:
		case <-time.After(5 * time.Second):
			t.Fatal("no attention raised")
		}
		// the looks a minute holds at 2 s apart, then the attention
		if looks := int(codexRegisterWait / codexRegisterEvery); runs.n != looks+1 {
			t.Fatalf("%d runs, want %d looks and the attention", runs.n, looks)
		}
		if !slices.Equal(last[:4], []string{"crew", "attention", "raise", "blocked"}) ||
			!strings.Contains(last[4], "AutoDoc can't wake this Codex session") || !strings.Contains(last[4], notFound.Error()) {
			t.Fatalf("the attention: %q", last)
		}
	})

	t.Run("another CLI's spawn runs none", func(t *testing.T) {
		a, c, _ := crewAgents(t, crewKinds)
		runs := &codexRuns{calls: make(chan []string, 4), answer: []error{nil}}
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
