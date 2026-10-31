package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// waitLooks stands in for the agent's autodoc in codexWaitLoop: each run's args are kept, and
// answered by answer in turn (past the list, the last); its stderr says why a look failed.
type waitLooks struct {
	args   [][]string
	answer []int
}

func (l *waitLooks) run(args []string, _, stderr io.Writer) int {
	l.args = append(l.args, args)
	if args[1] == "attention" {
		return 0
	}
	n := len(l.args) - 1
	for _, a := range l.args[:n] {
		if a[1] == "attention" {
			n--
		}
	}
	code := l.answer[min(n, len(l.answer)-1)]
	if code != 0 {
		_, _ = io.WriteString(stderr, "no Codex thread for this folder on the app-server yet\n")
	}
	return code
}

// TestCodexWaitLoopsUntilFoundEndedOrDone (docs/decisions/crew.md#wakers-and-the-wake-scheduler):
// a waiting `crew endpoint --codex-here` looks again until the folder's thread is found; a session
// that ended stops it with nothing raised; one never found within the wait raises the session's
// attention, blocked, saying why, and fails; a usage error is answered at once, never retried.
func TestCodexWaitLoopsUntilFoundEndedOrDone(t *testing.T) {
	ctxFile := filepath.Join(t.TempDir(), "ctx")
	if err := os.WriteFile(ctxFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rest := []string{"--codex-here", "--since", "100"}
	loop := func(l *waitLooks, wait time.Duration, file string) int {
		return codexWaitLoop(context.Background(), rest, wait, time.Millisecond, file, l.run, io.Discard, io.Discard)
	}

	found := &waitLooks{answer: []int{1, 1, 0}}
	if code := loop(found, time.Minute, ctxFile); code != 0 || len(found.args) != 3 ||
		!slices.Equal(found.args[0], []string{"crew", "endpoint", "--codex-here", "--since", "100"}) {
		t.Errorf("found on the third look: exit %d, runs %q", code, found.args)
	}

	usage := &waitLooks{answer: []int{2}}
	if code := loop(usage, time.Minute, ctxFile); code != 2 || len(usage.args) != 1 {
		t.Errorf("a usage error: exit %d after %d runs, want 2 at once", code, len(usage.args))
	}

	never := &waitLooks{answer: []int{1}}
	if code := loop(never, 20*time.Millisecond, ctxFile); code != 1 {
		t.Errorf("never found: exit %d, want 1", code)
	}
	last := never.args[len(never.args)-1]
	if len(never.args) < 2 || !slices.Equal(last[:4], []string{"crew", "attention", "raise", "blocked"}) ||
		!strings.Contains(last[4], "AutoDoc can't wake this Codex session: no Codex thread") {
		t.Errorf("never found: last run %q, want the session's attention raised, blocked, saying why", last)
	}

	ended := &waitLooks{answer: []int{1}}
	if code := loop(ended, time.Minute, filepath.Join(t.TempDir(), "gone")); code != 1 || len(ended.args) != 1 {
		t.Errorf("a session that ended: exit %d after runs %q, want one look and nothing raised", code, ended.args)
	}
}

// TestCrewEndpointWaitIsCodexHereAlone: --wait takes a duration and goes with --codex-here alone;
// anything else is usage (exit 2), before any look.
func TestCrewEndpointWaitIsCodexHereAlone(t *testing.T) {
	for _, args := range [][]string{{"--codex-here", "--wait"}, {"--codex-here", "--wait", "soon"}, {"--codex-here", "--wait=0s"},
		{"--claude", "--wait", "60s"}} {
		var errb bytes.Buffer
		if code := crewEndpointWaitMain(context.Background(), "", args, io.Discard, &errb); code != 2 {
			t.Errorf("%q: exit %d (%s), want usage", args, code, errb.String())
		}
	}
}
