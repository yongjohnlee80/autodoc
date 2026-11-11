package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/agents"
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

// A first run (never answered, no profile) opens the setup card in About's place; a profile, or an
// answer, opens About as before and never the card.
func TestTheSetupCardIsAFirstRunsOnly(t *testing.T) {
	t.Parallel()
	offeringSetup(t)
	fakeClaude(t)
	_, r, _ := runSetupTUI(t, t.TempDir(), nil, "", map[string]string{system.PrefAboutAt: "true"}, Options{})
	r.s.WaitForText(t, "Set up an AI agent?")
	if strings.Contains(r.s.String(), "about autodoc") {
		t.Fatal("About opened beside the setup card")
	}

	for name, prefs := range map[string]map[string]string{
		"a profile": {system.PrefAboutAt: "true"},
		"an answer": {system.PrefAboutAt: "true", agents.PrefAgentSetup: "declined"},
		"set up":    {system.PrefAboutAt: "true", agents.PrefAgentSetup: "done"},
	} {
		var profiles []agents.AgentProfile
		if name == "a profile" {
			profiles = []agents.AgentProfile{{Name: "Mine", Command: "true", Codename: "mine"}}
		}
		_, r, _ := runSetupTUI(t, t.TempDir(), profiles, "", prefs, Options{})
		r.s.WaitForText(t, "about autodoc")
		if strings.Contains(r.s.String(), "Set up an AI agent?") {
			t.Errorf("%s: the setup card opened", name)
		}
	}
}
