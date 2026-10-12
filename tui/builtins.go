package tui

// THE BUILT-IN COMMANDS — AutoDoc's own entries in the command catalog (catalog.go): what a menu
// row or a SPC key runs, each by its id. Until a feature leaves legacy, legacy contributes them
// here; a feature of its own declares its own (todos.go). Each Run calls the same function as the
// App.* command its Twin names, so a menu row, a key, ':' and autodoc.run do one thing.

// builtin is an entry that runs fn, the function its twin's App.* command runs.
func builtin(id, title, twin string, fn func()) Entry {
	return Entry{ID: id, Title: title, Help: title, Twin: twin, Run: func(Invocation) error { fn(); return nil }}
}

// Catalog is the host's own user commands.
func (l legacy) Catalog() []Entry { return l.h.builtinEntries() }

func (h *Host) builtinEntries() []Entry {
	return append([]Entry{
		// File
		builtin("file.new", "new file", "App.newFile()", h.newFile),
		builtin("file.open", "open a document", "App.openPicker()", h.openPicker),
		builtin("file.open_any", "open a file anywhere", "App.openFileDialog()", h.openFileDialog),
		builtin("file.save", "save", "App.save()", h.save),
		builtin("file.reload", "reload from disk", "App.reload()", h.reload),
		builtin("file.preview_html", "preview HTML", "App.previewHTML()", h.previewHTML),
		builtin("file.preview_diagram", "view the diagram", "App.previewDiagram()", h.previewDiagram),
		builtin("file.open_default_app", "open with the default app", "App.openWithDefaultApp()", h.openWithDefaultApp),
		builtin("file.back", "back", "App.goBack()", h.goBack),
		builtin("file.related", "related files, by digit", "App.openJumpCard()", h.openJumpCard),
		builtin("app.quit", "quit", "App.quit()", h.quit),
		builtin("app.command_line", "the command line", "App.openCommandLine()", h.openCommandLine),
		builtin("app.restart_backend", "restart the backend", "App.startRestart()", h.startRestart),
		// Go and Search
		builtin("search.open", "search the workspace", "App.openSearch()", h.openSearch),
		builtin("find.open", "find in the page", "App.openFindInPage()", h.openFindInPage),
		builtin("find.next", "find next", "App.findAgainNext()", func() { h.findAgain(+1) }),
		builtin("find.previous", "find previous", "App.findAgainPrevious()", func() { h.findAgain(-1) }),
		builtin("links.toggle", "the relations", "App.toggleLinks()", func() { h.togglePanel("links") }),
		builtin("agent.toggle", "AI agent", "App.toggleAgent()", h.toggleAgent),
		builtin("terminal.toggle", "the terminal", "App.toggleTerminal()", h.toggleTerminal),
		builtin("panel.arrange", "arrange a panel", "App.arrangePanel()", h.arrangePanel),
		builtin("panel.reset_layout", "reset the panel layout", "App.resetPanelLayout()", h.resetPanelLayout),
		builtin("workspace.switch", "switch workspace", "App.pickWorkspace()", h.pickWorkspace),
		builtin("workspace.manage", "manage workspaces", "App.manageWorkspaces()", h.manageWorkspaces),
		// View and the editor
		builtin("view.status_line", "the status line", "App.toggleStatusLine()", h.toggleStatusLine),
		builtin("view.menu", "show/focus or hide the menu", "App.leaderMenuBar()", h.leaderMenuBar),
		builtin("view.hide_menu_bar", "hide the menu bar", "App.toggleMenuBar()", h.toggleMenuBar),
		builtin("view.wrap", "wrap long lines", "App.toggleWrap()", h.toggleWrap),
		builtin("view.line_numbers", "line numbers", "App.toggleLineNumbers()", h.toggleLineNumbers),
		builtin("view.image_previews", "image previews", "App.toggleImagePreviews()", h.toggleImagePreviews),
		builtin("editor.mode", "the editor mode (Vim, Text)", "App.toggleKeymap()", h.toggleKeymap),
		builtin("editor.context_menu", "the right-click menu", "App.openContextMenu()", h.openContextMenu),
		builtin("editor.vim_keys", "the Vim keys' card", "App.toggleVimKeys()", h.toggleVimKeys),
		// Options, Plugins, System, Help
		builtin("prefs.editor", "editor preferences", "App.openPrefs()", h.openPrefs),
		builtin("prefs.fonts", "fonts and zoom", "App.openFonts()", h.openFonts),
		builtin("prefs.search_models", "search models", "App.openAIModels()", h.openAIModels),
		builtin("prefs.agent_profiles", "agent profiles", "App.openAgentProfiles()", h.openAgentProfiles),
		builtin("plugins.add", "add a plugin from a git URL", "App.startAddPlugin()", h.startAddPlugin),
		builtin("plugins.manage", "manage plugins", "App.managePlugins()", h.managePlugins),
		builtin("plugins.commands", "plugin commands", "pluginKeys.open()", func() { h.open("pluginKeys") }),
		builtin("help.keys", "help", "help.open()", func() { h.open("help") }),
		builtin("help.tutorial", "the tutorial", "App.openTutorial()", h.openTutorial),
		builtin("help.about", "about", "about.open()", func() { h.open("about") }),
	}, h.editorEntries()...)
}

// runEntry is App.run(id): a key running a catalog entry. What it cannot run is said, as a key's
// failure is.
func (h *Host) runEntry(id string) {
	if err := h.cat.run(id, nil); err != nil {
		h.notify(err.Error())
	}
}
