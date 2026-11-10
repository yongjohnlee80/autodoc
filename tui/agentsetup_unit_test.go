package tui

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/autodoc/tui/system"
)

// offeringSetup turns the first-run card on for one test's programs (TestMain turns it off for the
// rest).
func offeringSetup(t *testing.T) {
	t.Helper()
	tune(t).offerAgentSetup = true
}

func heardLines(path string) []string {
	b, _ := os.ReadFile(path)
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

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

// A first run (never answered, no profile) opens the setup card in About's place; a profile, or an
// answer, opens About as before and never the card.
func TestTheSetupCardIsAFirstRunsOnly(t *testing.T) {
	t.Parallel()
	offeringSetup(t)
	fakeClaude(t)
	_, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", map[string]string{system.PrefAboutAt: "true"}, Options{})
	r.s.WaitForText(t, "Set up an AI agent?")
	if strings.Contains(r.s.String(), "about autodoc") {
		t.Fatal("About opened beside the setup card")
	}

	for name, prefs := range map[string]map[string]string{
		"a profile": {system.PrefAboutAt: "true"},
		"an answer": {system.PrefAboutAt: "true", prefAgentSetup: "declined"},
		"set up":    {system.PrefAboutAt: "true", prefAgentSetup: "done"},
	} {
		var profiles []agentProfile
		if name == "a profile" {
			profiles = []agentProfile{{Name: "Mine", Command: "true", Codename: "mine"}}
		}
		_, r, _ := runAgentTUIWith(t, t.TempDir(), profiles, "", prefs, Options{})
		r.s.WaitForText(t, "about autodoc")
		if strings.Contains(r.s.String(), "Set up an AI agent?") {
			t.Errorf("%s: the setup card opened", name)
		}
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
