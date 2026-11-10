package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// The CLIs are offered in their fixed order, absent ones left out; none found is empty. Nothing runs.
func TestDetectAgentCLIsKeepsTheOrder(t *testing.T) {
	t.Parallel()
	on := func(names ...string) func(string) (string, error) {
		return func(c string) (string, error) {
			if slices.Contains(names, c) {
				return "/bin/" + c, nil
			}
			return "", errors.New("not found")
		}
	}
	name := func(cs []agentCLI) (out []string) {
		for _, c := range cs {
			out = append(out, c.command)
		}
		return out
	}
	if got := name(detectAgentCLIs(on("agy", "codex", "claude"))); !slices.Equal(got, []string{"claude", "codex", "agy"}) {
		t.Errorf("found %v; want claude, codex, agy in that order", got)
	}
	if got := detectAgentCLIs(on()); len(got) != 0 {
		t.Errorf("none found: %v", got)
	}
}

// AGENTS.md says what an agent can do for the user; the generated guide no longer has its own list.
func TestTheAgentsGuideHasTheJobs(t *testing.T) {
	t.Parallel()
	if !strings.Contains(autodoc.AgentsGuide, "## What you can do for the user") ||
		!strings.Contains(autodoc.AgentsGuide, "Proof-read as a professional editor") {
		t.Fatal("AGENTS.md has no \"What you can do for the user\" section")
	}
	guide := agentGuide(feature.Workspace{Name: "kb", Root: "/kb"}, "/bin/autodoc", "/autodoc.toml", "/w/bin/autodoc", "", crew.MailViaCLI)
	if strings.Count(guide, "What you can do for the user") != 1 || strings.Contains(guide, "## What to help with") {
		t.Fatal("the generated guide lists the jobs twice, or keeps its old list")
	}
}
