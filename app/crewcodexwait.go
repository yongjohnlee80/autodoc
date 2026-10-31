package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// codexWaitEvery is how often a waiting `crew endpoint --codex-here` looks for this folder's
// Codex thread again.
const codexWaitEvery = 2 * time.Second

// crewEndpointWaitMain runs `autodoc crew endpoint --codex-here [--since T] --wait D`
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): the agent's host runs it once, detached,
// as its Codex agent starts. It registers the folder's Codex session as `crew endpoint
// --codex-here` does, looking again every codexWaitEvery until the thread is found or D has passed;
// then it raises the session's attention, blocked, saying AutoDoc can't wake it (`crew attention
// raise blocked`), and fails. Every host's Codex agent registers the same way, with nothing to loop
// itself. A usage error is answered at once.
func crewEndpointWaitMain(ctx context.Context, configPath string, args []string, stdout, stderr io.Writer) int {
	var rest []string
	var wait time.Duration
	for i := 0; i < len(args); i++ {
		a := args[i]
		v, isWait := strings.CutPrefix(a, "--wait=")
		if a == "--wait" {
			if i+1 == len(args) {
				fmt.Fprintln(stderr, "usage: autodoc crew endpoint --codex-here [--since T] --wait D")
				return 2
			}
			v, isWait, i = args[i+1], true, i+1
		}
		if !isWait {
			rest = append(rest, a)
			continue
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			fmt.Fprintf(stderr, "autodoc: --wait %q: want a duration, such as 60s\n", v)
			return 2
		}
		wait = d
	}
	if !containsFlag(rest, "--codex-here") {
		fmt.Fprintln(stderr, "usage: --wait goes with crew endpoint --codex-here alone")
		return 2
	}
	deadline := time.Now().Add(wait)
	for {
		var errb bytes.Buffer
		code := crewMainIn(ctx, configPath, append([]string{"crew", "endpoint"}, rest...), nil, stdout, &errb)
		if code == 0 || code == 2 {
			_, _ = stderr.Write(errb.Bytes())
			return code
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			why := strings.TrimSpace(strings.Join(strings.Fields(errb.String()), " "))
			raise := []string{"crew", "attention", "raise", "blocked", attentionLine("AutoDoc can't wake this Codex session: " + why)}
			_ = crewMainIn(context.WithoutCancel(ctx), configPath, raise, nil, io.Discard, stderr)
			_, _ = stderr.Write(errb.Bytes())
			return code
		}
		// NOTE: can't announce: Codex starts its thread on its app-server without telling anyone, so
		// the folder's thread is looked for again on a timer until it is found or the wait ends.
		t := time.NewTimer(codexWaitEvery)
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.C:
		}
	}
}

// containsFlag reports whether args hold flag, bare or as flag=value.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// attentionLine is text as an attention carries it from a host's step: one line, at most 300
// bytes.
func attentionLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
