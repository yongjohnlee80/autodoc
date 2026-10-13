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
		// the registration restart's question (registrations.go)
		"App.registrationQuestion":    "",
		"App.canRestartRegistrations": false,
		"App.pageWidth":               defaultRuler + 2, // the ruler, the border, and the gutter (syncPageWidth)
		// semantic search's mark: shown once a status poll answers (progress.go)
		"App.semanticMark":   "",
		"App.semanticDot":    "default",
		"App.semanticLabel":  "",
		"App.semanticDetail": "",
		"App.searchTitle":    "search", // with semantic search's state once a poll answers
		"App.searchText":     "",       // the search field's words: set to clear them (clearSearch)
		"App.searchStatus":   "",       // the line under the field: blank, or what a search is waiting on (searchpick.go)
		// the search's stages' boxes (stages.go)
		"App.stageLexical":       true,
		"App.stageSemantic":      true,
		"App.stageSemanticShown": false,
		"App.stageRerank":        true,
		"App.stageRerankText":    "Rerank",
		"App.stageRerankShown":   false,

		// the Plugins menu (plugins.go)
		"App.fileTitle":         untitled,
		"App.diagramTitle":      "Mermaid preview",
		"App.diagramTextShown":  true,
		"App.diagramImageShown": false,
		"App.htmlPreviewTitle":  "HTML preview",
		"App.htmlPreviewHelp":   "",
		"App.diagramText":       "",
		"App.diagramHelp":       "",
		"App.wsProviders":       h.wsProviders,
		"App.wsProviderIndex":   0,
		"App.diagnosticsShown":  false,
		"App.diagnosticsLine":   "",
		"App.syntaxDefinition":  "Markdown",

		// the search picker
		"App.hits":                  h.searchPick.list.Model(),
		"App.hitsTitle":             "hits",
		"App.searchPreviewTitle":    "",
		"App.searchPreviewText":     "",
		"App.searchPreviewRendered": false,
		"App.searchPreviewAt":       0,
		// the open picker
		"App.pickerRows":       h.openPick.list.Model(),
		"App.openField":        "",
		"App.pickerStatus":     "files",
		"App.openPreviewTitle": "",
		"App.openPreviewText":  "",
		"App.openPreviewAt":    0,
		// the new-file picker
		"App.newFiles":        h.newPick.list.Model(),
		"App.newFilePath":     "",
		"App.fileNameError":   draftHelp,
		"App.newPreviewTitle": "",
		"App.newPreviewText":  "",
		"App.newPreviewAt":    0,

		// the leader card

		// the editor's preferences
		"App.keymaps":         choices(keymapLabels...),
		"App.themes":          choices(themeNames...),
		"App.edges":           choices(edges...),
		"App.yesNo":           choices("yes", "no"),
		"App.openAsChoices":   choices(openAsLabels...),
		"App.indentMarkModes": choices(indentMarkLabels...),
		"App.corners":         choices(cornerLabels...),
		"App.toastSeconds":    choices("1 s", "2 s", "3 s", "4 s", "5 s", "6 s", "7 s", "8 s", "9 s", "10 s"),
		"App.prefsError":      "",
		// the notifications' history (notify.go), and the Vim keys' card
		// find in a pane (find.go)
		"App.findTitle": "find",
		"App.findError": "",
		"App.lastFind":  "",
		// the workspace's models and their vectors (vectors.go)
		"App.vectors":         h.vectors.Model(),
		"App.vectorsTitle":    "vectors",
		"App.vectorsStatus":   "",
		"App.vectorsRefusals": "",

		// the workspace settings (settings.go): one dialog, Edit and Advanced, for an edit and an add
		"App.home":                  homeDir(),
		"App.browseFolder":          homeDir(),
		"App.settingsTitle":         "workspace settings",
		"App.settingsHelp":          "",
		"App.settingsTab":           0,
		"App.settingsAdding":        false,
		"App.settingsRootLine":      "",
		"App.settingsName":          "",
		"App.settingsRoot":          "",
		"App.settingsSchema":        "",
		"App.settingsSchemaState":   "",
		"App.settingsTexts":         "",
		"App.settingsCode":          "",
		"App.settingsDocs":          "",
		"App.settingsDocsOffered":   false,
		"App.settingsDocsLabel":     "",
		"App.settingsUnreadShown":   false,
		"App.settingsUnreadLine":    "",
		"App.settingsCodeOffered":   false,
		"App.settingsTextsNote":     "",
		"App.settingsTextsNoted":    false,
		"App.settingsCodeLabel":     "",
		"App.settingsInclude":       "",
		"App.settingsExclude":       "",
		"App.settingsMdIndex":       0,
		"App.settingsTxtIndex":      0,
		"App.settingsYamlIndex":     0,
		"App.settingsSection":       "",
		"App.policies":              choices(policyChoices...),
		"App.settingsPolicyIndex":   0,
		"App.settingsProviderState": "",
		"App.databasesShown":        false,
		"App.destinations":          choices(destinationLabels...),
		"App.settingsDestIndex":     0,
		"App.settingsDestDSN":       "",
		"App.settingsDestSchema":    "",
		"App.settingsDestState":     "",
		"App.vectorIndexes":         choices(indexLabels...),
		"App.settingsVectorIndex":   0,
		"App.sourceEngines":         choices(sourceLabels...),
		"App.settingsSourceIndex":   0,
		"App.settingsSrcDSN":        "",
		"App.settingsSrcSchema":     "",
		"App.settingsSourceState":   "",
		"App.settingsViewArgs":      "",

		"App.unsavedQuestion":  "",
		"App.recoveryQuestion": "",
		"App.htmlOpenQuestion": "",
		"App.conflictQuestion": "",
		"App.quitQuestion":     "The file has unsaved changes. Quit, and lose them?",
		"App.workspaces":       h.workspaces.Model(),

		"App.managed":        h.managed.Model(),
		"App.managerHelp":    managerHelp,
		"App.managerDetail":  "",
		"App.managerIndex":   0,
		"App.workspaceIndex": 0,

		"App.helpText":      helpText(h.keys, nil, nil), // the commands follow once attached (commandsChanged)
		"App.arrangeTitle":  "arrange a panel",
		"App.tutorialTitle": "", "App.tutorialText": "", "App.tutorialNextText": "&Next",
		"App.gui":           h.gui,
		"App.cellFonts":     h.cellFontModel,
		"App.proseFonts":    h.proseFontModel,
		"App.fontSizes":     choices(fontSizeLabels()...),
		"App.zoomSteps":     choices(zoomLabels()...),
		"App.cellFontIndex": 0, "App.proseFontIndex": 0,
	}
	for k, v := range h.keys.cardState() {
		st[k] = v
	}
	for k, v := range h.aboutState() {
		st[k] = v
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

// homeDir is where the folder picker starts: the home directory, or the root.
func homeDir() string {
	if d, err := os.UserHomeDir(); err == nil {
		return d
	}
	return "/"
}

// set publishes one source; a failure is kept for Run.
func (h *Host) set(name string, v any) { h.keep(h.p.Set(name, v)) }

// setField is the host's form fields written as a feature's are (endpoints.go: setField).
func (h *Host) setField(name, text string) { setField(core{h}, name, text) }

// setWhere is the status line's left: the editor's mode, then where the TUI is attached.
func (h *Host) setWhere(where string) {
	h.where = where
	h.set("App.statusLeft", h.core.Mode().String()+"  "+where)
}

// open opens a dialog the layout declares, by id.
// Every dialog opened or closed here supersedes a manager dialog whose fresh read is still on its
// way (withCurrent): the user has gone elsewhere, and the late answer must not pull them back.
func (h *Host) open(id string) {
	h.dialogSeq++
	h.keep(h.p.Call(id, "open"))
}

// closeDialog closes a dialog the layout declares, by id.
func (h *Host) closeDialog(id string) {
	h.dialogSeq++
	h.keep(h.p.Call(id, "close"))
}

// managerClosed is the workspace manager dismissed by its own Close or Esc: a dialog it asked for,
// still reading, does not open over whatever is beneath.
func (h *Host) managerClosed() { h.dialogSeq++ }
