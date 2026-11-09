package tui

import (
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"slices"
)

// THE BUILT-IN COMMANDS — AutoDoc's own entries in the command catalog (feature/catalog.go): what a menu
// row or a SPC key runs, each by its id. Until a feature leaves legacy, legacy contributes them
// here; a feature of its own declares its own (todos.go). Each Run calls the same function as the
// App.* command its Twin names, so a menu row, a key, ':' and autodoc.run do one thing.

// userOnly is e, the user's to run (Entry.UserOnly).
func userOnly(e feature.Entry) feature.Entry { e.UserOnly = true; return e }

// pathEntry is e, which opens or saves a path (Entry.Path).
func pathEntry(e feature.Entry) feature.Entry { e.Path = true; return e }

// Catalog is the host's own user commands.
func (l legacy) Catalog() []feature.Entry { return l.h.builtinEntries() }

func (h *Host) builtinEntries() []feature.Entry {
	return slices.Concat([]feature.Entry{
		// File
		feature.Builtin("file.new", "new file", "App.newFile()", h.newFile),
		feature.Builtin("panel.maximize", "maximize or restore the terminal or agents", "App.maximizePanel()", h.maximizePanel),
		feature.Builtin("panel.resume", "resume the terminal's or the agents' tab (SIGCONT)", "App.resumePanel()", h.resumePanel),
		feature.Builtin("file.open", "open a document", "App.openPicker()", h.openPicker),
		feature.Builtin("buffer.pick", "switch buffer", "App.openBuffers()", h.openBuffers),
		feature.Builtin("buffer.close", "close the buffer", "App.closeBuffer()", h.closeShownBuffer),
		feature.Builtin("buffer.close_right", "close the buffers to the right", "App.closeBuffersRight()", func() { h.closeBeside(1) }),
		feature.Builtin("buffer.close_left", "close the buffers to the left", "App.closeBuffersLeft()", func() { h.closeBeside(-1) }),
		feature.Builtin("file.open_any", "open a file anywhere", "App.openFileDialog()", h.openFileDialog),
		{ID: "file.open_path", Title: "open a file by its path", Verb: "open", Aliases: []string{"e", "edit"}, Args: "<path>",
			Help: "open a file by its path, as File › Open any file does once it is chosen: on the command line absolute, ~/… or relative to the workspace in use; from Lua, a key or a plugin, absolute",
			Run:  h.runOpenPath, Path: true},
		pathEntry(feature.Builtin("file.save", "save", "App.save()", h.save)),
		feature.Builtin("file.reload", "reload from disk", "App.reload()", h.reload),
		feature.Builtin("file.preview_html", "preview HTML", "App.previewHTML()", h.previewHTML),
		feature.Builtin("file.preview_diagram", "view the diagram", "App.previewDiagram()", h.previewDiagram),
		userOnly(feature.Builtin("file.open_default_app", "open with the default app", "App.openWithDefaultApp()", h.openWithDefaultApp)),
		pathEntry(feature.Builtin("file.follow_link", "go to the link under the cursor", "App.followLink()", h.followLink)),
		feature.Builtin("file.back", "back", "App.goBack()", h.goBack),
		feature.Builtin("app.quit", "quit", "App.quit()", h.quit),
		feature.Builtin("app.command_line", "the command line", "App.openCommandLine()", h.openCommandLine),
		feature.Builtin("app.restart_backend", "restart the backend", "App.startRestart()", h.startRestart),
		// Go and Search
		feature.Builtin("search.open", "search the workspace", "App.openSearch()", h.openSearch),
		feature.Builtin("find.open", "find in the page", "App.openFindInPage()", h.openFindInPage),
		feature.Builtin("find.next", "find next", "App.findAgainNext()", func() { h.findAgain(+1) }),
		feature.Builtin("find.previous", "find previous", "App.findAgainPrevious()", func() { h.findAgain(-1) }),
		feature.Builtin("panel.arrange", "arrange a panel", "App.arrangePanel()", h.arrangePanel),
		feature.Builtin("panel.reset_layout", "reset the panel layout", "App.resetPanelLayout()", h.resetPanelLayout),
		feature.Builtin("workspace.switch", "switch workspace", "App.pickWorkspace()", h.pickWorkspace),
		feature.Builtin("workspace.manage", "manage workspaces", "App.manageWorkspaces()", h.manageWorkspaces),
		{ID: "kbtype.apply", Title: "apply a KB type to this workspace", Args: "<name or git URL>", UserOnly: true,
			Help: "create a KB type's folders and files in the open workspace, keeping every file already there: autovim.kb, zettelkasten.kb, para.kb, slipbox.kb, or a type's git URL",
			Run:  h.kbTypeApply},
		// View and the editor
		feature.Builtin("view.status_line", "the status line", "App.toggleStatusLine()", h.toggleStatusLine),
		feature.Builtin("view.menu", "show/focus or hide the menu", "App.leaderMenuBar()", h.leaderMenuBar),
		feature.Builtin("view.hide_menu_bar", "hide the menu bar", "App.toggleMenuBar()", h.toggleMenuBar),
		feature.Builtin("view.wrap", "wrap long lines", "App.toggleWrap()", h.toggleWrap),
		feature.Builtin("view.line_numbers", "line numbers", "App.toggleLineNumbers()", h.toggleLineNumbers),
		feature.Builtin("view.image_previews", "image previews", "App.toggleImagePreviews()", h.toggleImagePreviews),
		{ID: "view.redraw", Title: "draw the Rendered view again",
			Help: "lay the Rendered view out again, the note unchanged: what it draws from beside the note changed (a lab note's cell outputs, which the Jupyter plugin runs this for)",
			Run:  func(feature.Invocation) error { h.redrawRendered(); return nil }},
		feature.Builtin("editor.mode", "the editor mode (Vim, Text)", "App.toggleKeymap()", h.toggleKeymap),
		feature.Builtin("editor.context_menu", "the right-click menu", "App.openContextMenu()", h.openContextMenu),
		feature.Builtin("editor.vim_keys", "the Vim keys' card", "App.toggleVimKeys()", h.toggleVimKeys),
		// Options, Plugins, System, Help
		feature.Builtin("prefs.editor", "editor preferences", "App.openPrefs()", h.openPrefs),
		feature.Builtin("prefs.fonts", "fonts and zoom", "App.openFonts()", h.openFonts),
		feature.Builtin("prefs.terminal_panel_keys", "Ctrl+h/j/k/l move between panels while typing", "App.toggleTerminalPanelKeys()", h.toggleTerminalPanelKeys),
		feature.Builtin("help.keys", "help", "help.open()", func() { h.open("help") }),
		feature.Builtin("help.tutorial", "the tutorial", "App.openTutorial()", h.openTutorial),
		feature.Builtin("help.about", "about", "about.open()", func() { h.open("about") }),
	}, h.editorEntries(), h.bufferEntries(), h.bufferSlotEntries(), h.widgetEntries(), h.layoutEntries())
}

// runEntry is App.run(id): a key running a catalog entry. What it cannot run is said, as a key's
// failure is.
func (h *Host) runEntry(id string) {
	if err := h.cat.Run(id, nil); err != nil {
		h.notify(err.Error())
	}
}
