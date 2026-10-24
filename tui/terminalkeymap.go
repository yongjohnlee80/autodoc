package tui

import tuidecl "github.com/yongjohnlee80/golib/tui/decl"

// TERMINALS FOR A USER WHO NEVER LEARNT VIM — under the text editor's keymap the terminal and agent
// panels' shells never stay in normal mode unasked, where i would be needed to type again: the
// wheel's scrollback view ends at the next key, which goes to the program (golib's modeless
// scroll). Normal mode asked for (the mode key) stays, and the strip says how to get back while a
// tab is in it. Under Vim's keymap the terminals are as they always were.

// terminalPanels are the panels whose tabs follow the keymap: the terminal panel's and the agents'.
var terminalPanels = []string{"terminalView", "agentView"}

// applyTerminalKeymap gives both panels' tabs p's keymap: modeless scroll and the normal-mode hint
// under "text", neither under "vim". A rebuilt panel gets them again (reloaded).
func (h *Host) applyTerminalKeymap(p prefs) {
	text := p.keymap == "text"
	hint := ""
	if text {
		hint = h.normalModeHint()
	}
	for _, id := range terminalPanels {
		c, ok := h.p.Find(id)
		if !ok {
			continue
		}
		tabs, ok := tuidecl.TerminalTabsOf(c)
		if !ok {
			continue
		}
		tabs.SetModelessScroll(text)
		tabs.SetNormalHint(hint)
	}
}

// normalModeHint is how to get back to typing from normal mode: the mode key as the keymap binds it
// now, or i alone when a scheme unbinds it.
func (h *Host) normalModeHint() string {
	if keys := h.keys.keysValue(terminalModeID); len(keys) > 0 {
		return h.trf("autodoc.notice.terminal.normal_mode_hint", keys[0])
	}
	return h.tr("autodoc.notice.terminal.normal_mode_hint_i")
}
