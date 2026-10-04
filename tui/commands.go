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
		"App.save":           none(h.save),
		"App.reload":         none(h.reload),
		"App.previewHTML":    none(h.previewHTML),
		"App.previewDiagram": none(h.previewDiagram),
		"App.zoomIn":         none(func() { h.zoomPreview(1) }),
		"App.zoomOut":        none(func() { h.zoomPreview(-1) }),
		"App.newFile":        none(h.newFile),
		"App.createFile":     oneString("App.createFile", "a path", h.createFile),
		"App.quit":           none(h.quit),
		"App.quitConfirmed":  none(func() { h.p.Quit() }),
		"App.edited":         none(h.edited),
		"App.syncMode":       none(h.syncMode),
		"App.unsaved":        oneString("App.unsaved", "save, discard or stay", h.unsaved),
		"App.conflict":       oneString("App.conflict", "keep, reload or overwrite", h.conflict),

		// the pickers
		"App.openSearch":    none(h.openSearch),
		"App.searchLive":    oneString("App.searchLive", "a query", h.searchLive),
		"App.searchClosed":  none(h.searchClosed),
		"App.previewHit":    oneNumber("App.previewHit", "a row", h.previewHit),
		"App.openHit":       oneNumber("App.openHit", "a row", h.openHit),
		"App.openPicker":    none(h.openPicker),
		"App.pickerFilter":  oneString("App.pickerFilter", "a filter", h.pickerFilter),
		"App.previewPick":   oneNumber("App.previewPick", "a row", h.previewPick),
		"App.pickerSelect":  oneNumber("App.pickerSelect", "a row", h.pickerSelect),
		"App.newFileFilter": oneString("App.newFileFilter", "a path", h.newFileFilter),
		"App.previewNew":    oneNumber("App.previewNew", "a row", h.previewNew),
		"App.newFileFolder": oneNumber("App.newFileFolder", "a row", h.newFileFolder),

		// the plugins
		"App.openPlugin":            oneString("App.openPlugin", "a plugin", h.openPlugin),
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
		"App.openBacklink":      oneNumber("App.openBacklink", "a row", h.openBacklink),

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
		"App.saveProvider":            fiveStrings("App.saveProvider", "a name, the base URL, the model, the key and the context window", h.saveProvider),
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
