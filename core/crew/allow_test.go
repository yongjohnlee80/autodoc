package crew_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// theGuidesCommands is the allowance's list as ruled (docs/decisions/crew.md#wake-acceptance-not-tool-approvals),
// written out here rather than read from crew.GuideCommands, so a command added there fails a cell.
var theGuidesCommands = []string{"mail receive", "mail ack", "mail reply", "mail send", "mail inbox", "mail show",
	"crew whoami", "crew list", "crew endpoint", "crew attention", "crew bridge", "--contracts"}

// TestTheAllowanceIsExactlyTheGuidesCommands: each kind's allowance names exactly the guide's own
// commands, each one, never `autodoc *` or any other: Claude's as Bash rules (and only those read as
// AutoDoc's, so a host never takes out a rule the user wrote), OpenCode's as permission.bash.
func TestTheAllowanceIsExactlyTheGuidesCommands(t *testing.T) {
	var claude []string
	ocWant := map[string]any{}
	for _, c := range theGuidesCommands {
		claude = append(claude, "Bash(autodoc "+c+":*)")
		ocWant["autodoc "+c] = "allow"
		ocWant["autodoc "+c+" *"] = "allow"
	}
	if got := crew.ClaudeAllow(); !slices.Equal(got, claude) {
		t.Errorf("Claude's allowance %q, want exactly %q", got, claude)
	}
	oc := crew.OpenCodeAllow()
	if len(oc) != len(ocWant) {
		t.Errorf("OpenCode's allowance %v, want exactly %v", oc, ocWant)
	}
	for k, v := range ocWant {
		if oc[k] != v {
			t.Errorf("OpenCode's allowance %v lacks %q: %q", oc, k, v)
		}
	}
	for _, r := range crew.ClaudeAllow() {
		if !crew.IsClaudeAllow(r) || strings.Contains(r, "autodoc *") || strings.Contains(r, "autodoc:") {
			t.Errorf("a rule %q is too wide or not read as AutoDoc's", r)
		}
	}
	for _, user := range []string{"Bash(git status:*)", "Bash(autodocx:*)", "Read(~/notes/**)", "Bash(autodoc search:*)",
		"Bash(autodoc --call:*)", "Bash(autodoc mail send)", "Bash(autodoc mail:*)"} {
		if crew.IsClaudeAllow(user) {
			t.Errorf("%q, the user's, is read as AutoDoc's: a host would take it out", user)
		}
	}
}
