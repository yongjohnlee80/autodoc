package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// envValue is name's value in environ as a process reads it: the last one wins.
func envValue(environ []string, name string) (string, bool) {
	v, ok := "", false
	for _, kv := range environ {
		if k, val, found := strings.Cut(kv, "="); found && k == name {
			v, ok = val, true
		}
	}
	return v, ok
}

// TestAnAgentRunsInAutoDocsTerminalWhateverStartedAutoDoc: an agent's TERM and COLORTERM are the
// terminal AutoDoc gives it, never inherited. AutoDoc.app started from the Dock has no TERM, and its
// Claude and Antigravity agents drew no colour (reported 2026-11-06, v0.3.11; the shell terminal
// beside them, started by golib with its own TERM, had colour). Started from tmux, AutoDoc has
// tmux's TERM, which isn't the agent's terminal either. A profile's own env still wins.
func TestAnAgentRunsInAutoDocsTerminalWhateverStartedAutoDoc(t *testing.T) {
	for _, inherited := range []string{"", "tmux-256color"} {
		started := procenv.Env(nil).With("TERM", "COLORTERM") // AutoDoc's environment, as it was started
		if inherited != "" {
			started = started.With("TERM=" + inherited)
		}
		env := agentEnviron(started, &agentInstance{})
		if v, _ := envValue(env, "TERM"); v != "xterm-256color" {
			t.Errorf("AutoDoc started with TERM %q: the agent's TERM is %q, want xterm-256color", inherited, v)
		}
		if v, _ := envValue(env, "COLORTERM"); v != "truecolor" {
			t.Errorf("AutoDoc started with TERM %q: the agent's COLORTERM is %q, want truecolor", inherited, v)
		}
	}

	own := agentEnviron(nil, &agentInstance{env: []string{"TERM=xterm-direct"}})
	if v, _ := envValue(own, "TERM"); v != "xterm-direct" {
		t.Errorf("a profile's TERM: the agent's is %q, want the profile's xterm-direct", v)
	}
	if !slices.Contains(own, "COLORTERM=truecolor") {
		t.Error("a profile setting TERM lost AutoDoc's COLORTERM")
	}
}
