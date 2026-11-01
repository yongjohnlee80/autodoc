package crew_test

import (
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestEverySetupAllowsTheContracts: the written guide prints a verb's table with `autodoc
// --contracts` (token item 2), read-only and with no daemon, so every kind whose setup allows
// commands allows it, in either mail mode: else the guide's slimming moves its tokens into
// permission prompts. Claude by its Bash rule, OpenCode's allowance (written once its probe passes),
// agy by its command rule.
func TestEverySetupAllowsTheContracts(t *testing.T) {
	const claudeRule, agyRule = "Bash(autodoc --contracts:*)", "command(autodoc --contracts)"
	full := claudeOnPath(t, everyClaudeEvent...)
	servers := []crew.MCPServer{{Name: "autodoc", Command: []string{"/x/autodoc", "crew", "mcp"}, Tools: crew.MCPToolNames()}}
	for _, mcp := range []bool{false, true} {
		in := crew.SetupInput{Kind: crew.KindClaude, Host: crew.HostWindow, Autodoc: "a", Env: map[string]string{"PATH": full}, Dir: t.TempDir(), DataDir: t.TempDir()}
		if mcp {
			in.Servers = servers
		}
		s, err := crew.SetupFor(in, "")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(s.Allow, claudeRule) {
			t.Errorf("Claude's allowance (MCP mail %v) %q lacks %s", mcp, s.Allow, claudeRule)
		}
	}
	if oc := crew.OpenCodeAllow(); oc["autodoc --contracts"] != "allow" || oc["autodoc --contracts *"] != "allow" {
		t.Errorf("OpenCode's allowance %v lacks the contracts", oc)
	}
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindAgy, Host: crew.HostWindow, Autodoc: "a", AgentDir: t.TempDir(), DataDir: t.TempDir(), Servers: servers}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.Allow, agyRule) {
		t.Errorf("agy's allowance %q lacks %s", s.Allow, agyRule)
	}
}
