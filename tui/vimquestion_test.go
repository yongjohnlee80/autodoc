//go:build linux || darwin

package tui

import (
	"strings"
	"testing"
)

// offeringVimQuestion turns the first run's Vim question on for one test (TestMain turns it off for
// the rest).
func offeringVimQuestion(t *testing.T) {
	t.Helper()
	offerVimQuestion = true
	t.Cleanup(func() { offerVimQuestion = false })
}

// A true first run (no keymap and no agent setup ever stored, no profile) asks "Do you use Vim?"
// first: neither the agent setup's card nor About opens beside it. No (n) stores the text editor's
// keys, and the setup's card follows; a second start asks nothing.
func TestTheVimQuestionIsATrueFirstRunsFirstCard(t *testing.T) {
	offeringVimQuestion(t)
	offeringSetup(t)
	fakeClaude(t)
	// the setup's card looks for the agent CLIs as it opens (openAgentSetup): counted, it says
	// whether the card opened beneath the question, which the question's own card would hide
	looked, look := 0, lookAgentCLI
	lookAgentCLI = func(c string) (string, error) { looked++; return look(c) }
	d, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", map[string]string{prefAboutAt: "true"}, Options{})
	r.s.WaitForText(t, "Do you use Vim?")
	if sc := r.s.String(); strings.Contains(sc, "Set up an AI agent?") || strings.Contains(sc, "about autodoc") {
		t.Fatalf("another card opened beside the Vim question:\n%s", sc)
	}
	if n := onLoop(r, func() int { return looked }); n != 0 {
		t.Fatalf("the agent setup's card opened beneath the Vim question (%d CLI lookups)", n)
	}
	r.keys(t, key('n')) // &No, a text editor's keys
	storedPref(t, d, r, prefKeymap, "text")
	r.s.WaitForText(t, "Set up an AI agent?")
	if strings.Contains(r.s.String(), "Do you use Vim?") {
		t.Error("the Vim question stayed open under the setup's card")
	}
}

// Yes (y) keeps Vim's keys, stored, so the question is not asked again; Escape is No's.
func TestTheVimQuestionsYesAndEscape(t *testing.T) {
	offeringVimQuestion(t)
	d, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", nil, Options{})
	r.s.WaitForText(t, "Do you use Vim?")
	r.keys(t, key('y')) // &Yes, Vim's keys
	storedPref(t, d, r, prefKeymap, "vim")
	r.s.WaitFor(t, "the card closed", func(sc string) bool { return !strings.Contains(sc, "Do you use Vim?") })

	d, r, _ = runAgentTUIWith(t, t.TempDir(), nil, "", nil, Options{})
	r.s.WaitForText(t, "Do you use Vim?")
	r.keys(t, esc())
	storedPref(t, d, r, prefKeymap, "text")
}

// An existing user is never asked: a keymap stored, the agent setup answered, or a profile, each
// opens the run as before.
func TestTheVimQuestionIsNotAskedOfAnExistingUser(t *testing.T) {
	offeringVimQuestion(t)
	for name, prefs := range map[string]map[string]string{
		"a keymap":  {prefAboutAt: "true", prefKeymap: "vim"},
		"an answer": {prefAboutAt: "true", prefAgentSetup: "declined"},
		"a profile": {prefAboutAt: "true"},
	} {
		var profiles []agentProfile
		if name == "a profile" {
			profiles = []agentProfile{{Name: "Mine", Command: "true", Codename: "mine"}}
		}
		_, r, _ := runAgentTUIWith(t, t.TempDir(), profiles, "", prefs, Options{})
		r.s.WaitForText(t, "about autodoc")
		if strings.Contains(r.s.String(), "Do you use Vim?") {
			t.Errorf("%s: the Vim question was asked", name)
		}
	}
}

// vimQuestionPending reads the stored keymap, not the default's: a store with none asks, one with
// either keymap does not.
func TestTheVimQuestionReadsTheStoredKeymap(t *testing.T) {
	offeringVimQuestion(t)
	for stored, want := range map[string]bool{"": true, "vim": false, "text": false} {
		m := map[string]any{}
		if stored != "" {
			m[prefKeymap] = stored
		}
		if got := vimQuestionPending(prefsForPanels(m, false, nil)); got != want {
			t.Errorf("keymap %q stored: pending %v, want %v", stored, got, want)
		}
	}
}
