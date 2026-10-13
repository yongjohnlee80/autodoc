package tui

// THE TERMINALS' LOOK — Theme and font…, on the terminal's and the agents' tab menus (tabmenus.go):
// a small dialog of the two settings the terminals have of their own. They are the very settings
// Preferences' terminal row (the theme) and Options › Fonts and zoom… (the font) set, through the
// same sources and commands: one setting each, shown and set from any of the three. A choice shows
// on both panels at once; OK keeps it, and Esc puts back what was chosen when the dialog opened.
// The font is a window's: in a terminal, which draws in its own, the dialog says so instead.

// lookWas is the terminals' look as the dialog opened, for Esc to put back.
type lookWas struct {
	theme, font string
	open        bool
}

// openTerminalLook is Theme and font…: the dialog, over what is chosen now.
func (h *Host) openTerminalLook() {
	h.lookWas = lookWas{theme: h.prefs.termTheme, font: h.prefs.termFont, open: true}
	note := ""
	if h.fonts != nil {
		h.listFonts()
	} else {
		note = "the font is a window's: a terminal draws in its own"
	}
	h.set("App.terminalLookNote", note)
	h.open("terminalLook")
}

// terminalLookKept is OK: what was chosen stays.
func (h *Host) terminalLookKept() { h.lookWas.open = false }

// terminalLookCancelled is Esc: what was chosen when the dialog opened, back.
func (h *Host) terminalLookCancelled() {
	was := h.lookWas
	if !was.open {
		return
	}
	h.lookWas.open = false
	if h.prefs.termTheme != was.theme {
		h.setPref(prefTermTheme, was.theme, func(p *prefs) { p.termTheme = was.theme })
	}
	if h.prefs.termFont != was.font {
		h.setPref(prefTermFont, was.font, func(p *prefs) { p.termFont = was.font })
	}
}
