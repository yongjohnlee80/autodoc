package tui

import (
	"fmt"
	"strconv"

	"github.com/yongjohnlee80/golib/decl"
	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE APP SINGLETON'S COMMANDS — what the document invokes.
//
// The table is the whole of it: a handler in main.qml or a component file can reach exactly these,
// by these names, and nothing else of the program.
func (h *Host) commands() map[string]decl.HandlerFunc {
	return map[string]decl.HandlerFunc{
		"App.save":               none(h.save),
		"App.reload":             none(h.reload),
		"App.previewHTML":        none(h.previewHTML),
		"App.previewDiagram":     none(h.previewDiagram),
		"App.openWithDefaultApp": none(h.openWithDefaultApp),
		"App.zoomIn":             none(func() { h.zoomPreview(1) }),
		"App.zoomOut":            none(func() { h.zoomPreview(-1) }),
		"App.newFile":            none(h.newFile),
		"App.openFileDialog":     none(h.openFileDialog),
		"App.openAbsolute":       oneString("App.openAbsolute", "a path", h.openAbsolute),
		"App.createAbsolute":     oneString("App.createAbsolute", "a path", h.createAbsolute),
		"App.createFile":         oneString("App.createFile", "a path", h.createFile),
		"App.quit":               none(h.quit),
		"App.quitConfirmed":      none(func() { h.p.Quit() }),
		"App.edited":             none(h.edited),
		"App.syncMode":           none(h.syncMode),
		"App.unsaved":            oneString("App.unsaved", "save, discard or stay", h.unsaved),
		"App.openHTMLAs":         oneString("App.openHTMLAs", "simplified, raw or cancel", h.openHTMLAs),
		"App.conflict":           oneString("App.conflict", "keep, reload or overwrite", h.conflict),

		// the pickers
		"App.openSearch":           none(h.openSearch),
		"App.searchLive":           oneString("App.searchLive", "a query", h.searchLive),
		"App.searchClosed":         none(h.searchClosed),
		"App.openRecent":           none(h.openRecent),
		"App.recentSelect":         oneNumber("App.recentSelect", "a row", h.recentSelect),
		"App.clearRecent":          none(h.clearRecent),
		"App.toggleAgent":          none(h.toggleAgent),
		"App.openAgentProfiles":    none(h.openAgentProfiles),
		"App.startAddAgent":        none(h.startAddAgent),
		"App.startEditAgent":       oneNumber("App.startEditAgent", "a row", h.startEditAgent),
		"App.saveAgent":            twoStrings("App.saveAgent", "a name and a command", h.saveAgent),
		"App.removeAgent":          oneNumber("App.removeAgent", "a row", h.removeAgent),
		"App.makeAgentDefault":     oneNumber("App.makeAgentDefault", "a row", h.makeAgentDefault),
		"App.useAgent":             oneNumber("App.useAgent", "a row", h.useAgent),
		"App.switchAgentConfirmed": none(h.switchAgentConfirmed),
		"App.agentExited":          oneNumber("App.agentExited", "an exit code", h.agentExited),
		"App.panelResized":         stringAndTwoNumbers("App.panelResized", "a panel and its size and length", h.panelResized),
		"App.panelPlaced":          stringAndFourNumbers("App.panelPlaced", "a panel and its x, y, width and height", h.panelPlaced),
		"App.arrangePanel":         none(h.arrangePanel),
		"App.arrangeStep":          oneString("App.arrangeStep", "a step", h.arrangeStep),
		"App.arrangeKeep":          none(h.arrangeKeep),
		"App.arrangeCancel":        none(h.arrangeCancel),
		"App.resetPanelLayout":     none(h.resetPanelLayout),
		"App.openFonts":            none(h.openFonts),
		"App.openContextMenu":      none(h.openContextMenu),
		"App.openTutorial":         none(h.openTutorial),
		"App.tutorialPage":         oneString("App.tutorialPage", "back or next", h.tutorialPage),
		"App.setCellFontIndex":     oneNumber("App.setCellFontIndex", "a row", h.setCellFontIndex),
		"App.setProseFontIndex":    oneNumber("App.setProseFontIndex", "a row", h.setProseFontIndex),
		"App.setFontSizeIndex":     oneNumber("App.setFontSizeIndex", "a row", h.setFontSizeIndex),
		"App.setZoomIndex":         oneNumber("App.setZoomIndex", "a row", h.setZoomIndex),
		"App.setZoom":              oneNumber("App.setZoom", "a percent", h.setZoom),
		"App.zoomWindow":           oneString("App.zoomWindow", "in or out", h.zoomWindow),
		"App.toggleSearchStage":    oneString("App.toggleSearchStage", "lexical, semantic or rerank", h.toggleSearchStage),
		"App.previewHit":           oneNumber("App.previewHit", "a row", h.previewHit),
		"App.openHit":              oneNumber("App.openHit", "a row", h.openHit),
		"App.openPicker":           none(h.openPicker),
		"App.pickerFilter":         oneString("App.pickerFilter", "a filter", h.pickerFilter),
		"App.previewPick":          oneNumber("App.previewPick", "a row", h.previewPick),
		"App.pickerSelect":         oneNumber("App.pickerSelect", "a row", h.pickerSelect),
		"App.newFileFilter":        oneString("App.newFileFilter", "a path", h.newFileFilter),
		"App.previewNew":           oneNumber("App.previewNew", "a row", h.previewNew),
		"App.newFileFolder":        oneNumber("App.newFileFolder", "a row", h.newFileFolder),

		// the plugins
		"App.openPlugin":            oneString("App.openPlugin", "a plugin", h.openPlugin),
		"App.pluginEntry":           oneString("App.pluginEntry", "a plugin's menu row or letter", h.pluginEntry),
		"App.pluginKey":             oneString("App.pluginKey", "a plugin command letter", h.pluginKey),
		"App.startAddPlugin":        none(h.startAddPlugin),
		"App.addPlugin":             oneString("App.addPlugin", "a git URL", h.addPlugin),
		"App.pluginConfirmed":       none(h.pluginConfirmed),
		"App.pluginDeclined":        none(h.pluginDeclined),
		"App.managePlugins":         none(h.managePlugins),
		"App.startUpdatePlugin":     oneNumber("App.startUpdatePlugin", "a row", h.startUpdatePlugin),
		"App.startRemovePlugin":     oneNumber("App.startRemovePlugin", "a row", h.startRemovePlugin),
		"App.placePlugin":           oneNumber("App.placePlugin", "a row", h.placePlugin),
		"App.removePluginConfirmed": none(h.removePluginConfirmed),

		// the panels
		"App.toggleExplorer":    none(func() { h.togglePanel("explorer") }),
		"App.toggleLinks":       none(func() { h.togglePanel("links") }),
		"App.toggleTerminal":    none(h.toggleTerminal),
		"App.terminalExited":    oneNumber("App.terminalExited", "an exit code", h.terminalExited),
		"App.setTerminalEdge":   oneNumber("App.setTerminalEdge", "a row", h.setTerminalEdge),
		"App.setTerminalSize":   oneNumber("App.setTerminalSize", "a row", h.setTerminalSize),
		"App.setTerminalLength": oneNumber("App.setTerminalLength", "a row", h.setTerminalLength),
		"App.setAgentEdge":      oneNumber("App.setAgentEdge", "a row", h.setAgentEdge),
		"App.setAgentSize":      oneNumber("App.setAgentSize", "a row", h.setAgentSize),
		"App.setAgentLength":    oneNumber("App.setAgentLength", "a row", h.setAgentLength),
		"App.panelOpened":       oneString("App.panelOpened", "a panel", h.panelOpened),
		"App.panelClosed":       oneString("App.panelClosed", "a panel", h.panelClosed),
		"App.movePane":          oneString("App.movePane", "h, j, k or l", h.movePane),
		"App.explorerActivated": oneIndex("App.explorerActivated", h.explorerActivated),
		"App.explorerMoved":     oneIndex("App.explorerMoved", h.explorerMoved),
		"App.linksMoved":        oneNumber("App.linksMoved", "a row", h.linksMoved),
		"App.openFind":          none(h.openFind),
		"App.openNotices":       none(h.openNotices),
		"App.toggleVimKeys":     none(h.toggleVimKeys),
		"App.noticesClosed":     none(h.noticesClosed),
		"App.clearNotices":      none(h.clearNotices),
		"App.find":              oneString("App.find", "a word or phrase", h.startFind),
		"App.findCancelled":     none(h.findCancelled),
		"App.findNext":          none(h.findNext),
		"App.openFindInPage":    none(h.openFindInPage),
		"App.findAgainNext":     none(func() { h.findAgain(+1) }),
		"App.findAgainPrevious": none(func() { h.findAgain(-1) }),
		"App.clearFind":         none(h.clearFind),
		"App.findPrevious":      none(h.findPrevious),
		"App.openRelation":      oneNumber("App.openRelation", "a row", h.openRelation),
		"App.relationsDepth":    none(h.relationsDepth),
		"App.openJumpCard":      none(h.openJumpCard),
		"App.jumpTo":            oneNumber("App.jumpTo", "1 to 9", h.jumpTo),
		"App.goBack":            none(h.goBack),

		// the workspaces
		"App.pickWorkspace":            none(h.pickWorkspace),
		"App.useWorkspace":             oneNumber("App.useWorkspace", "a row", h.useWorkspace),
		"App.manageWorkspaces":         none(h.manageWorkspaces),
		"App.startAddWorkspace":        none(h.startAddWorkspace),
		"App.startEditWorkspace":       oneNumber("App.startEditWorkspace", "a row", h.startEditWorkspace),
		"App.startAdvancedWorkspace":   oneNumber("App.startAdvancedWorkspace", "a row", h.startAdvancedWorkspace),
		"App.managerMoved":             oneNumber("App.managerMoved", "a row", h.managerMoved),
		"App.settingsTabMoved":         oneNumber("App.settingsTabMoved", "a tab", h.settingsTabMoved),
		"App.saveSettings":             settingsArgs(h.saveSettings),
		"App.browseRoot":               oneString("App.browseRoot", "a directory", h.browseRoot),
		"App.rootChosen":               oneString("App.rootChosen", "a directory", h.rootChosen),
		"App.managerClosed":            none(h.managerClosed),
		"App.openOutline":              none(h.openOutline),
		"App.outlineFilter":            oneString("App.outlineFilter", "a filter", h.outlineFilter),
		"App.previewHeading":           oneNumber("App.previewHeading", "a row", h.previewHeading),
		"App.jumpToHeading":            oneNumber("App.jumpToHeading", "a row", h.jumpToHeading),
		"App.cursorMoved":              none(h.cursorMoved),
		"App.htmlLink":                 oneString("App.htmlLink", "a link's target", h.htmlLink),
		"App.startRemoveWorkspace":     oneNumber("App.startRemoveWorkspace", "a row", h.startRemoveWorkspace),
		"App.removeWorkspaceConfirmed": none(h.removeWorkspaceConfirmed),

		// the backend
		"App.startRestart":            none(h.startRestart),
		"App.restartConfirmed":        none(h.restartConfirmed),
		"App.restartMismatch":         none(h.restartMismatch),
		"App.quitMismatch":            none(h.quitMismatch),
		"App.restartForRegistrations": none(h.restartForRegistrations),

		// the preferences
		"App.openPrefs":            none(h.openPrefs),
		"App.openActiveSettings":   none(h.openActiveSettings),
		"App.openAIModels":         none(h.openAIModels),
		"App.setKeymap":            oneString("App.setKeymap", "vim or text", h.setKeymap),
		"App.setKeymapIndex":       oneNumber("App.setKeymapIndex", "a row", h.setKeymapIndex),
		"App.toggleKeymap":         none(h.toggleKeymap),
		"App.useTheme":             oneString("App.useTheme", "a theme's name", h.useTheme),
		"App.setThemeIndex":        oneNumber("App.setThemeIndex", "a row", h.setThemeIndex),
		"App.toggleMenuBar":        none(h.toggleMenuBar),
		"App.leaderMenuBar":        none(h.leaderMenuBar),
		"App.toggleStatusLine":     none(h.toggleStatusLine),
		"App.toggleWrap":           none(h.toggleWrap),
		"App.toggleLineNumbers":    none(h.toggleLineNumbers),
		"App.setWrapIndex":         oneNumber("App.setWrapIndex", "a row", h.setWrapIndex),
		"App.setImagesIndex":       oneNumber("App.setImagesIndex", "a row", h.setImagesIndex),
		"App.toggleImagePreviews":  none(h.toggleImagePreviews),
		"App.openPreviewInBrowser": none(h.openPreviewInBrowser),
		"App.previewClosed":        none(h.previewClosed),
		"App.setLineNumbersIndex":  oneNumber("App.setLineNumbersIndex", "a row", h.setLineNumbersIndex),
		"App.setToastCorner":       oneNumber("App.setToastCorner", "a row", h.setToastCorner),
		"App.setToastSeconds":      oneNumber("App.setToastSeconds", "a row", h.setToastSeconds),
		"App.setMenuHiddenIndex":   oneNumber("App.setMenuHiddenIndex", "a row", h.setMenuHiddenIndex),
		"App.setStatusShownIndex":  oneNumber("App.setStatusShownIndex", "a row", h.setStatusShownIndex),
		"App.setExplorerEdge":      oneNumber("App.setExplorerEdge", "a row", h.setExplorerEdge),
		"App.setLinksEdge":         oneNumber("App.setLinksEdge", "a row", h.setLinksEdge),
		"App.setRuler":             oneString("App.setRuler", "a number of columns", h.setRuler),

		// the embedding providers
		"App.showProvider":            oneNumber("App.showProvider", "a row", h.providerDetail),
		"App.useProvider":             oneNumber("App.useProvider", "a row", h.useProvider),
		"App.stopSemantic":            none(h.stopSemantic),
		"App.startAddProvider":        none(h.startAddProvider),
		"App.startEditProvider":       oneNumber("App.startEditProvider", "a row", h.startEditProvider),
		"App.startRemoveProvider":     oneNumber("App.startRemoveProvider", "a row", h.startRemoveProvider),
		"App.removeProviderConfirmed": none(h.removeProviderConfirmed),
		"App.providerKindChosen":      numberAndString("App.providerKindChosen", "a row and the base URL", h.providerKindChosen),
		"App.listModels":              twoStrings("App.listModels", "the base URL and the key", h.listModels),
		"App.pickModel":               oneNumber("App.pickModel", "a row", h.pickModel),
		"App.cancelIndexing":          none(h.cancelIndexing),
		"App.openVectors":             none(h.openVectors),
		"App.startPurge":              oneNumber("App.startPurge", "a row", h.startPurge),
		"App.purgeConfirmed":          none(h.purgeConfirmed),
		// the ranker models, and the buttons AI models' tabs share
		"App.aiTabMoved":            oneNumber("App.aiTabMoved", "a tab", h.aiTabMoved),
		"App.aiAdd":                 none(h.aiAdd),
		"App.aiEdit":                oneNumber("App.aiEdit", "a row", h.aiEdit),
		"App.aiUse":                 oneNumber("App.aiUse", "a row", h.aiUse),
		"App.aiRemove":              oneNumber("App.aiRemove", "a row", h.aiRemove),
		"App.showRanker":            oneNumber("App.showRanker", "a row", h.rankerDetail),
		"App.startEditRanker":       oneNumber("App.startEditRanker", "a row", h.startEditRanker),
		"App.stopRanking":           none(h.stopRanking),
		"App.startRankerWindow":     none(h.startRankerWindow),
		"App.saveRankerWindow":      oneString("App.saveRankerWindow", "a number of candidates", h.saveRankerWindow),
		"App.removeRankerConfirmed": none(h.removeRankerConfirmed),
		"App.rankerKindChosen":      numberAndString("App.rankerKindChosen", "a row and the base URL", h.rankerKindChosen),
		"App.checkRanker":           twoStrings("App.checkRanker", "the base URL and the key", h.checkRanker),
		"App.saveRanker":            fourStrings("App.saveRanker", "a name, the base URL, the model and the key", h.saveRanker),
		"App.saveProvider":          fiveStrings("App.saveProvider", "a name, the base URL, the model, the key and the context window", h.saveProvider),
	}
}

// none is a command that takes no arguments, and refuses any it is given.
func none(fn func()) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) > 0 {
			return fmt.Errorf("takes no arguments, and was given %d", len(args))
		}
		fn()
		return nil
	}
}

func oneString(name, what string, fn func(string)) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) != 1 || args[0].Kind != qml.SpecValueString {
			return fmt.Errorf("%s takes %s", name, what)
		}
		fn(args[0].Raw)
		return nil
	}
}

func twoStrings(name, what string, fn func(string, string)) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) != 2 || args[0].Kind != qml.SpecValueString || args[1].Kind != qml.SpecValueString {
			return fmt.Errorf("%s takes %s", name, what)
		}
		fn(args[0].Raw, args[1].Raw)
		return nil
	}
}

func oneNumber(name, what string, fn func(int)) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) != 1 || args[0].Kind != qml.SpecValueNumber {
			return fmt.Errorf("%s takes %s", name, what)
		}
		n, err := strconv.Atoi(args[0].Raw)
		if err != nil {
			return fmt.Errorf("%s takes %s, not %s", name, what, args[0].Raw)
		}
		fn(n)
		return nil
	}
}

func fourStrings(name, what string, fn func(a, b, c, d string)) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) != 4 {
			return fmt.Errorf("%s takes %s", name, what)
		}
		for _, a := range args {
			if a.Kind != qml.SpecValueString {
				return fmt.Errorf("%s takes %s", name, what)
			}
		}
		fn(args[0].Raw, args[1].Raw, args[2].Raw, args[3].Raw)
		return nil
	}
}

func fiveStrings(name, what string, fn func(a, b, c, d, e string)) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) != 5 {
			return fmt.Errorf("%s takes %s", name, what)
		}
		for _, a := range args {
			if a.Kind != qml.SpecValueString {
				return fmt.Errorf("%s takes %s", name, what)
			}
		}
		fn(args[0].Raw, args[1].Raw, args[2].Raw, args[3].Raw, args[4].Raw)
		return nil
	}
}

// stringAndTwoNumbers is a command taking a name and two whole numbers: a panel and its size and
// length.
func stringAndTwoNumbers(name, what string, fn func(string, int, int)) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) != 3 || args[0].Kind != qml.SpecValueString || args[1].Kind != qml.SpecValueNumber || args[2].Kind != qml.SpecValueNumber {
			return fmt.Errorf("%s takes %s", name, what)
		}
		a, err1 := strconv.Atoi(args[1].Raw)
		b, err2 := strconv.Atoi(args[2].Raw)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("%s takes %s, not %s and %s", name, what, args[1].Raw, args[2].Raw)
		}
		fn(args[0].Raw, a, b)
		return nil
	}
}

// stringAndFourNumbers is a name and four numbers, fractions allowed: a panel's placement.
func stringAndFourNumbers(name, what string, fn func(string, float64, float64, float64, float64)) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) != 5 || args[0].Kind != qml.SpecValueString {
			return fmt.Errorf("%s takes %s", name, what)
		}
		var n [4]float64
		for i, a := range args[1:] {
			v, err := strconv.ParseFloat(a.Raw, 64)
			if a.Kind != qml.SpecValueNumber || err != nil {
				return fmt.Errorf("%s takes %s, not %s", name, what, a.Raw)
			}
			n[i] = v
		}
		fn(args[0].Raw, n[0], n[1], n[2], n[3])
		return nil
	}
}

func numberAndString(name, what string, fn func(int, string)) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) != 2 || args[1].Kind != qml.SpecValueString {
			return fmt.Errorf("%s takes %s", name, what)
		}
		n, err := strconv.Atoi(args[0].Raw)
		if err != nil {
			return fmt.Errorf("%s takes %s", name, what)
		}
		fn(n, args[1].Raw)
		return nil
	}
}

// oneIndex is a command that takes a view's row Index, as a tree's signal gives it.
func oneIndex(name string, fn func(tuidecl.Index) error) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		if len(args) != 1 {
			return fmt.Errorf("%s takes a row's index", name)
		}
		ix, ok := args[0].Obj.(tuidecl.Index)
		if !ok {
			return fmt.Errorf("%s takes a row's index, not %s", name, args[0].Raw)
		}
		return fn(ix)
	}
}
