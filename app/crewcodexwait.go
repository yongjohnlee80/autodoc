package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
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
// raise blocked`), and fails. Once registered, it starts the session's Codex bridge, which reports
// its permission prompts (crew bridge --codex, detached). A session that ended meanwhile (its context, $AUTODOC_CREW_CONTEXT,
// gone) stops it with nothing raised. Every host's Codex agent registers the same way, with
// nothing to loop itself. A usage error is answered at once.
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
	run := func(args []string, stdout, stderr io.Writer) int {
		return crewMainIn(context.WithoutCancel(ctx), configPath, args, nil, stdout, stderr)
	}
	ctxFile := os.Getenv("AUTODOC_CREW_CONTEXT")
	return codexRegisterThenReport(ctx, rest, wait, codexWaitEvery, ctxFile, run, stdout, stderr)
}

// codexRegisterThenReport is codexWaitLoop, then, once the session is registered, the start of its
// Codex bridge (`crew bridge --codex`, with rest's --since: crewCodexBridgeMain, which detaches), so
// its permission prompts are reported from then on. A bridge that fails to start leaves the
// registration standing: its prompts read as unreported, and no Enter is pressed into them.
func codexRegisterThenReport(ctx context.Context, rest []string, wait, every time.Duration, ctxFile string,
	run func(args []string, stdout, stderr io.Writer) int, stdout, stderr io.Writer) int {
	code := codexWaitLoop(ctx, rest, wait, every, ctxFile, run, stdout, stderr)
	if code == 0 {
		_ = run(append([]string{"crew", "bridge", "--codex"}, sinceArgs(rest)...), stdout, stderr)
	}
	return code
}

// sinceArgs are rest's --since and its value, as the bridge takes them (none when rest has none).
func sinceArgs(rest []string) []string {
	for i, a := range rest {
		if v, ok := strings.CutPrefix(a, "--since="); ok {
			return []string{"--since", v}
		}
		if a == "--since" && i+1 < len(rest) {
			return []string{"--since", rest[i+1]}
		}
	}
	return nil
}

// codexWaitLoop is crewEndpointWaitMain's loop over `crew endpoint <rest>`, each run by run: every
// every, until it succeeds, is usage, the session's context (ctxFile) is gone, or wait has passed,
// when the session's attention is raised, blocked. It answers the exit code.
func codexWaitLoop(ctx context.Context, rest []string, wait, every time.Duration, ctxFile string,
	run func(args []string, stdout, stderr io.Writer) int, stdout, stderr io.Writer) int {
	deadline := time.Now().Add(wait)
	for {
		var errb bytes.Buffer
		code := run(append([]string{"crew", "endpoint"}, rest...), stdout, &errb)
		if code == 0 || code == 2 {
			_, _ = stderr.Write(errb.Bytes())
			return code
		}
		if ctxFile != "" {
			if _, err := os.Stat(ctxFile); err != nil { // the session ended: nothing left to register or raise
				_, _ = stderr.Write(errb.Bytes())
				return code
			}
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			why := strings.TrimSpace(strings.Join(strings.Fields(errb.String()), " "))
			text := attentionLine("AutoDoc can't wake this Codex session: " + why)
			_ = run([]string{"crew", "attention", "raise", "blocked", text}, io.Discard, stderr)
			_, _ = stderr.Write(errb.Bytes())
			return code
		}
		// NOTE: can't announce: Codex starts its thread on its app-server without telling anyone, so
		// the folder's thread is looked for again on a timer until it is found or the wait ends.
		t := time.NewTimer(every)
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
