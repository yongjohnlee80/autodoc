package tui

// THE APP SINGLETON'S STATE — what the document reads.
//
// Each is a SOURCE, so changing one repaints exactly the bindings that read it. The host changes
// them only through set, so what the screen says and what the host knows cannot drift apart.

func (h *Host) state() map[string]any {
	st := map[string]any{
		"App.status":       "",
		"App.statusLeft":   "NORMAL  autodoc [connecting]",
		"App.statusCenter": "",
		"App.keyset":       "vim",

		"App.results":      h.results,
		"App.resultsTitle": "notes",
		"App.noteTitle":    "no note",
		"App.noNote":       true,
		"App.backlinks":    h.backlinks,
		"App.linksTitle":   "backlinks",

		"App.searchHelp":    "Enter searches · the hits replace the notes pane",
		"App.lastQuery":     "",
		"App.pickerRows":    h.picker,
		"App.pickerStatus":  "type to filter · Enter opens",
		"App.noteNameError": "a path in the workspace; .md is added when it has none",

		"App.unsavedQuestion":  "",
		"App.conflictQuestion": "",
		"App.quitQuestion":     "The note has unsaved changes. Quit, and lose them?",
		"App.workspaces":       h.workspaces,

		"App.managed":              h.managed,
		"App.managerHelp":          managerHelp,
		"App.workspaceAddError":    addHelp,
		"App.workspaceRenameError": "",
		"App.renameFrom":           "",
		"App.removeQuestion":       "",

		"App.helpText":  helpText,
		"App.aboutText": h.aboutText(),
	}
	for k, v := range themeState(h.theme) {
		st[k] = v
	}
	return st
}

// set publishes one source; a failure is kept for Run.
func (h *Host) set(name string, v any) { h.keep(h.p.Set(name, v)) }

// setWhere is the status line's left: the editor's mode, then where the TUI is attached.
func (h *Host) setWhere(where string) {
	h.where = where
	h.set("App.statusLeft", h.editor.Mode().String()+"  "+where)
}

// setStatus puts a message on the status line's right, beside the progress while there is any.
func (h *Host) setStatus(msg string) {
	h.message = msg
	h.publishStatus()
}

// open opens a dialog the layout declares, by id.
func (h *Host) open(id string) { h.keep(h.p.Call(id, "open")) }

// closeDialog closes a dialog the layout declares, by id.
func (h *Host) closeDialog(id string) { h.keep(h.p.Call(id, "close")) }
