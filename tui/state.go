package tui

import (
	"os"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE APP SINGLETON'S STATE — what the document reads.
//
// Each is a SOURCE, so changing one repaints exactly the bindings that read it. The host changes
// them only through set, so what the screen says and what the host knows cannot drift apart.

func (h *Host) state() map[string]any {
	st := map[string]any{
		"App.status":             "",
		"App.statusLeft":         "NORMAL  autodoc [connecting]",
		"App.statusCenter":       "",
		"App.mismatchQuestion":   "",
		"App.canRestartMismatch": false,
		"App.pageWidth":          defaultRuler + 2, // the ruler, the border, and the gutter (syncPageWidth)
		// semantic search's mark: shown once a status poll answers (progress.go)
		"App.semanticMark":   "",
		"App.semanticDot":    "default",
		"App.semanticLabel":  "",
		"App.semanticDetail": "",
		"App.searchTitle":    "search", // with semantic search's state once a poll answers
		"App.searchText":     "",       // the search field's words: set to clear them (clearSearch)
		"App.searchStatus":   "",       // the line under the field: blank, or what a search is waiting on (pickers.go)

		"App.explorer": h.explorer,
		// the Plugins menu (plugins.go)
		"App.plugins":              h.pluginRows,
		"App.pluginUrl":            "",
		"App.pluginRisk":           pluginRisk,
		"App.pluginConfirmTitle":   "",
		"App.pluginQuestion":       "",
		"App.managedPlugins":       h.managedPlugins,
		"App.pluginsHelp":          pluginsHelp,
		"App.removePluginQuestion": "",
		"App.noteTitle":            untitled,
		"App.fileTypesTitle":       "file types",
		"App.fileTypesRoot":        "",
		"App.fileTypesPatterns":    "",
		"App.fileTypesHelp":        "",
		"App.diagramTitle":         "Mermaid preview",
		"App.diagramText":          "",
		"App.diagramHelp":          "",
		"App.patternTitle":         "workspace rules",
		"App.patternRoot":          "",
		"App.patternInclude":       "",
		"App.patternExclude":       "",
		"App.patternHelp":          "",
		"App.markdownTypeIndex":    0,
		"App.textTypeIndex":        0,
		"App.yamlTypeIndex":        0,
		"App.syntaxDefinition":     "Markdown (find)",
		"App.backlinks":            h.backlinks,
		"App.linksTitle":           "backlinks",

		// the search picker
		"App.hits":               h.hits,
		"App.hitsTitle":          "hits",
		"App.searchPreviewTitle": "",
		"App.searchPreviewText":  "",
		"App.searchPreviewAt":    0,
		// the open picker
		"App.pickerRows":       h.picker,
		"App.pickerStatus":     "notes",
		"App.openPreviewTitle": "",
		"App.openPreviewText":  "",
		"App.openPreviewAt":    0,
		// the new-note picker
		"App.newNotes":        h.newList,
		"App.newNotePath":     "",
		"App.noteNameError":   newNoteHelp,
		"App.newPreviewTitle": "",
		"App.newPreviewText":  "",
		"App.newPreviewAt":    0,

		// the leader card
		"App.leaderText": leaderText,

		// the editor's preferences
		"App.keymaps":           choices(keymapLabels...),
		"App.keymapIndex":       0,
		"App.themes":            choices(themeNames...),
		"App.themeIndex":        0,
		"App.edges":             choices(edges...),
		"App.explorerEdgeIndex": 0,
		"App.linksEdgeIndex":    1,
		"App.yesNo":             choices("yes", "no"),
		"App.menuHiddenIndex":   0,
		"App.statusShownIndex":  1,
		"App.wrapIndex":         0,
		"App.lineNumbersIndex":  1,
		"App.corners":           choices(cornerLabels...),
		"App.toastCornerIndex":  0,
		"App.toastSeconds":      choices("1 s", "2 s", "3 s", "4 s", "5 s", "6 s", "7 s", "8 s", "9 s", "10 s"),
		"App.toastSecondsIndex": defaultToastSeconds - 1,
		"App.prefsError":        "",
		// the notifications' history (notify.go), and the Vim keys' card
		"App.notices":         h.noticeList,
		"App.noticesTitle":    "notifications (0)",
		"App.providers":       h.providers,
		"App.providersStatus": "",
		"App.providersTitle":  "embedding providers",
		"App.providerDetail":  "",
		// the provider form
		"App.providerKinds":          kindChoices(),
		"App.providerFormTitle":      "",
		"App.providerFormError":      "",
		"App.providerKindIndex":      0,
		"App.providerName":           "",
		"App.providerBase":           "",
		"App.providerModel":          "",
		"App.providerKey":            "",
		"App.providerKeyShown":       false,
		"App.providerKeyLabel":       "",
		"App.providerContext":        "",
		"App.providerContextHint":    "",
		"App.providerSectionHint":    "",
		"App.providerContextShown":   false,
		"App.providerModels":         h.providerModels,
		"App.providerModelsStatus":   "",
		"App.providerRemoveQuestion": "",
		"App.restartQuestion":        "",
		// find in a pane (find.go)
		"App.findTitle":  "find",
		"App.findError":  "",
		"App.lastFind":   "",
		"App.linksIndex": 0,
		// the workspace's models and their vectors (vectors.go)
		"App.vectors":         h.vectors,
		"App.vectorsTitle":    "vectors",
		"App.vectorsStatus":   "",
		"App.vectorsRefusals": "",
		"App.purgeQuestion":   "",

		// the workspace add
		"App.home":    homeDir(),
		"App.wsTitle": "untitled",

		"App.unsavedQuestion":  "",
		"App.conflictQuestion": "",
		"App.quitQuestion":     "The note has unsaved changes. Quit, and lose them?",
		"App.workspaces":       h.workspaces,

		"App.managed":              h.managed,
		"App.managerHelp":          managerHelp,
		"App.workspaceRenameError": "",
		"App.renameFrom":           "",
		"App.sectionTitle":         "section size",
		"App.sectionSize":          "512",
		"App.sectionError":         "",
		"App.policyTitle":          "embedding",
		"App.embeddingPolicy":      "always",
		"App.policyError":          "",
		"App.workspaceIndex":       0,
		"App.removeQuestion":       "",

		"App.helpText":  helpText,
		"App.aboutText": h.aboutText(),
	}
	for k, v := range themeState(h.theme) {
		st[k] = v
	}
	for k, v := range prefState(h.prefs) {
		st[k] = v
	}
	st["App.statusShown"] = h.statusShown()
	return st
}

// choices are a chooser's rows: each a label.
func choices(labels ...string) *tuidecl.ListModel {
	m := tuidecl.NewListModel("key", "label")
	rows := make([]rowOf, len(labels))
	for i, l := range labels {
		rows[i] = rowOf{"key": l, "label": l}
	}
	m.Reset(rows)
	return m
}

func kindChoices() *tuidecl.ListModel {
	labels := make([]string, len(providerKinds))
	for i, k := range providerKinds {
		labels[i] = k.label
	}
	return choices(labels...)
}

// homeDir is where the folder picker starts: the home directory, or the root.
func homeDir() string {
	if d, err := os.UserHomeDir(); err == nil {
		return d
	}
	return "/"
}

// set publishes one source; a failure is kept for Run.
func (h *Host) set(name string, v any) { h.keep(h.p.Set(name, v)) }

// setWhere is the status line's left: the editor's mode, then where the TUI is attached.
func (h *Host) setWhere(where string) {
	h.where = where
	h.set("App.statusLeft", h.editor.Mode().String()+"  "+where)
}

// open opens a dialog the layout declares, by id.
func (h *Host) open(id string) { h.keep(h.p.Call(id, "open")) }

// closeDialog closes a dialog the layout declares, by id.
func (h *Host) closeDialog(id string) { h.keep(h.p.Call(id, "close")) }
