package tui

import "github.com/yongjohnlee80/autodoc/tui/feature"

// THE FIRST RUN'S VIM QUESTION — before the agent setup's card (agentsetup.go), a true first run
// asks whether the user uses Vim. Yes is the Vim keymap; No is the text editor's, whose terminals
// never stay in normal mode unasked (terminalkeymap.go). It is asked once, and only when neither the
// keymap nor the agent setup was ever stored; Preferences change the keymap later, as always.

// vimQuestionPending is the question's condition, read in one place: a true first run, with no
// keymap and no agent setup ever stored, and no agent profile.
func vimQuestionPending(s *seams, p feature.PrefsView) bool {
	return s.offerVimQuestion && !asPrefs(p).keymapChosen && p.Value(prefAgentSetup) == "" &&
		len(agentProfilesOf(p.Value(prefAgentProfiles))) == 0
}

// answerVimQuestion is the card's answer: "vim" (Yes) or anything else, the text editor's keys (No,
// and Escape). The keymap is stored, so the question is not asked again; then the run's next card,
// the agent setup's, opens when it is pending.
func (h *Host) answerVimQuestion(answer string) {
	keymap := "text"
	if answer == "vim" {
		keymap = "vim"
	}
	h.setKeymap(keymap)
	h.closeDialog("vimQuestion")
	if agentSetupPending(&h.seams, h.prefs) {
		h.agents.openAgentSetup()
	}
}
