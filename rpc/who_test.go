package rpc

import (
	"regexp"
	"testing"
)

// A CLI's hello says who runs it (ADR 1792015069 §2.6): its agent, pid and cwd go in its token, so a
// todo event names its caller; another client's token, or a CLI that sent nothing, is as before.
func TestACLISaysWhoItIsInItsToken(t *testing.T) {
	info := map[string]any{"agent": "agent:reed-richards:1", "pid": int64(42), "cwd": "/src/lm#x"}
	if got, want := who(CLIName, info), "(agent=agent:reed-richards:1 pid=42 cwd=/src/lmx)"; got != want {
		t.Errorf("who %q, want %q", got, want)
	}
	tok := newClientToken(CLIName + who(CLIName, info))
	if !regexp.MustCompile(`^autodoc-cli\(agent=agent:reed-richards:1 pid=42 cwd=/src/lmx\)#[0-9a-f]{8}$`).MatchString(tok) {
		t.Errorf("token %q", tok)
	}
	for _, c := range []struct {
		name string
		info map[string]any
	}{{TUIName, info}, {CLIName, nil}, {CLIName, map[string]any{"pid": "42"}}} {
		if got := who(c.name, c.info); got != "" {
			t.Errorf("who(%s, %v) = %q, want none", c.name, c.info, got)
		}
	}
}
