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
		"App.searchStatus":   "",       // the line under the field: blank, or what a search is waiting on (pickers.go)
		// the search's stages' boxes (stages.go)
		"App.stageLexical":       true,
		"App.stageSemantic":      true,
		"App.stageSemanticShown": false,
		"App.stageRerank":        true,
		"App.stageRerankText":    "Rerank",
		"App.stageRerankShown":   false,

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
		"App.fileTitle":            untitled,
		"App.diagramTitle":         "Mermaid preview",
		"App.diagramTextShown":     true,
		"App.diagramImageShown":    false,
		"App.htmlPreviewTitle":     "HTML preview",
		"App.htmlPreviewHelp":      "",
		"App.imagesIndex":          0,
		"App.diagramText":          "",
		"App.diagramHelp":          "",
		"App.wsProviders":          h.wsProviders,
		"App.wsProviderIndex":      0,
		"App.diagnosticsShown":     false,
		"App.diagnosticsLine":      "",
		"App.syntaxDefinition":     "Markdown (find)",
		"App.backlinks":            h.backlinks,
		"App.linksTitle":           "backlinks",

		// the search picker
		"App.hits":               h.hits,
		"App.hitsTitle":          "hits",
		"App.searchPreviewTitle": "",
		"App.searchPreviewText":  "",
		"App.searchPreviewAt":    0,
		// the outline picker (outline.go)
		"App.outlineRows":         h.outlineList,
		"App.outlineStatus":       "headings",
		"App.outlinePreviewTitle": "",
		"App.outlinePreviewText":  "",
		"App.outlinePreviewAt":    0,
		// the open picker
		"App.pickerRows":       h.picker,
		"App.pickerStatus":     "files",
		"App.openPreviewTitle": "",
		"App.openPreviewText":  "",
		"App.openPreviewAt":    0,
		// the new-file picker
		"App.newFiles":        h.newList,
		"App.newFilePath":     "",
		"App.fileNameError":   newFileHelp,
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
		// the terminal (terminal.go): its folder, whether it shows, and its choosers
		"App.terminalDir":         "",
		"App.terminalShown":       false,
		"App.terminalEdges":       choices(termEdgeLabels...),
		"App.terminalEdgeIndex":   0,
		"App.terminalSizes":       choices(percentLabels(termSizes)...),
		"App.terminalSizeIndex":   nearest(termSizes, 30),
		"App.terminalLengths":     choices(percentLabels(termLengths)...),
		"App.terminalLengthIndex": nearest(termLengths, 100),
		"App.yesNo":               choices("yes", "no"),
		"App.menuHiddenIndex":     0,
		"App.statusShownIndex":    1,
		"App.wrapIndex":           0,
		"App.lineNumbersIndex":    1,
		"App.corners":             choices(cornerLabels...),
		"App.toastCornerIndex":    0,
		"App.toastSeconds":        choices("1 s", "2 s", "3 s", "4 s", "5 s", "6 s", "7 s", "8 s", "9 s", "10 s"),
		"App.toastSecondsIndex":   defaultToastSeconds - 1,
		"App.prefsError":          "",
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
		// the recent files (recent.go)
		"App.recentFiles":  h.recentModel,
		"App.recentStatus": "",
		// the agent terminal and its profiles (agent.go, agentprofiles.go)
		"App.agentTitle":          "agent",
		"App.agentShown":          false,
		"App.agentEdge":           "center",
		"App.agentProfiles":       h.agentRows,
		"App.agentProfilesStatus": "",
		"App.agentFormTitle":      "",
		"App.agentFormError":      "",
		"App.agentName":           "",
		"App.agentCommand":        "",
		"App.agentSwitchQuestion": "",
		// the ranker models (rankers.go)
		"App.aiTab":                0,
		"App.aiEmbeddingTab":       true,
		"App.aiRankerTab":          false,
		"App.aiRankerChoice":       false,
		"App.aiUseEnabled":         true,
		"App.rankers":              h.rankers,
		"App.rankersStatus":        "",
		"App.rankerDetail":         "",
		"App.rankerKinds":          choices(rankerKindChoices()...),
		"App.rankerFormTitle":      "",
		"App.rankerFormError":      "",
		"App.rankerKindIndex":      0,
		"App.rankerName":           "",
		"App.rankerBase":           "",
		"App.rankerModel":          "",
		"App.rankerModelShown":     false,
		"App.rankerKey":            "",
		"App.rankerKeyLabel":       "",
		"App.rankerModelsStatus":   "",
		"App.rankerRemoveQuestion": "",
		"App.rankerWindow":         "",
		"App.rankerWindowError":    "",
		"App.restartQuestion":      "",
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
		"App.settingsCodeOffered":   false,
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
		"App.conflictQuestion": "",
		"App.quitQuestion":     "The file has unsaved changes. Quit, and lose them?",
		"App.workspaces":       h.workspaces,

		"App.managed":        h.managed,
		"App.managerHelp":    managerHelp,
		"App.managerDetail":  "",
		"App.managerIndex":   0,
		"App.workspaceIndex": 0,
		"App.removeQuestion": "",

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
