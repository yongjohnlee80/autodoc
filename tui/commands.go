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
		"App.save":          none(h.save),
		"App.reload":        none(h.reload),
		"App.newNote":       none(h.newNote),
		"App.createNote":    oneString("App.createNote", "a path", h.createNote),
		"App.quit":          none(h.quit),
		"App.quitConfirmed": none(func() { h.p.Quit() }),
		"App.edited":        none(h.edited),
		"App.syncMode":      none(h.syncMode),
		"App.unsaved":       oneString("App.unsaved", "save, discard or stay", h.unsaved),
		"App.conflict":      oneString("App.conflict", "keep, reload or overwrite", h.conflict),

		// the pickers
		"App.openSearch":    none(h.openSearch),
		"App.searchLive":    oneString("App.searchLive", "a query", h.searchLive),
		"App.previewHit":    oneNumber("App.previewHit", "a row", h.previewHit),
		"App.openHit":       oneNumber("App.openHit", "a row", h.openHit),
		"App.openPicker":    none(h.openPicker),
		"App.pickerFilter":  oneString("App.pickerFilter", "a filter", h.pickerFilter),
		"App.previewPick":   oneNumber("App.previewPick", "a row", h.previewPick),
		"App.pickerSelect":  oneNumber("App.pickerSelect", "a row", h.pickerSelect),
		"App.newNoteFilter": oneString("App.newNoteFilter", "a path", h.newNoteFilter),
		"App.previewNew":    oneNumber("App.previewNew", "a row", h.previewNew),
		"App.newNoteFolder": oneNumber("App.newNoteFolder", "a row", h.newNoteFolder),

		// the panels
		"App.toggleExplorer":    none(func() { h.togglePanel("explorer") }),
		"App.toggleLinks":       none(func() { h.togglePanel("links") }),
		"App.panelOpened":       oneString("App.panelOpened", "a panel", h.panelOpened),
		"App.panelClosed":       oneString("App.panelClosed", "a panel", h.panelClosed),
		"App.movePane":          oneString("App.movePane", "h, j, k or l", h.movePane),
		"App.explorerActivated": oneIndex("App.explorerActivated", h.explorerActivated),
		"App.openBacklink":      oneNumber("App.openBacklink", "a row", h.openBacklink),

		// the workspaces
		"App.pickWorkspace":            none(h.pickWorkspace),
		"App.useWorkspace":             oneNumber("App.useWorkspace", "a row", h.useWorkspace),
		"App.manageWorkspaces":         none(h.manageWorkspaces),
		"App.startAddWorkspace":        none(h.startAddWorkspace),
		"App.addWorkspace":             twoStrings("App.addWorkspace", "a name and a root", h.addWorkspace),
		"App.startRenameWorkspace":     oneNumber("App.startRenameWorkspace", "a row", h.startRenameWorkspace),
		"App.renameWorkspace":          oneString("App.renameWorkspace", "a name", h.renameWorkspace),
		"App.startRemoveWorkspace":     oneNumber("App.startRemoveWorkspace", "a row", h.startRemoveWorkspace),
		"App.removeWorkspaceConfirmed": none(h.removeWorkspaceConfirmed),

		// the backend
		"App.startRestart":     none(h.startRestart),
		"App.restartConfirmed": none(h.restartConfirmed),

		// the preferences
		"App.openPrefs":           none(h.openPrefs),
		"App.openAIModels":        none(h.openAIModels),
		"App.setKeymap":           oneString("App.setKeymap", "vim or text", h.setKeymap),
		"App.setKeymapIndex":      oneNumber("App.setKeymapIndex", "a row", h.setKeymapIndex),
		"App.toggleKeymap":        none(h.toggleKeymap),
		"App.useTheme":            oneString("App.useTheme", "a theme's name", h.useTheme),
		"App.setThemeIndex":       oneNumber("App.setThemeIndex", "a row", h.setThemeIndex),
		"App.toggleMenuBar":       none(h.toggleMenuBar),
		"App.toggleStatusLine":    none(h.toggleStatusLine),
		"App.setMenuHiddenIndex":  oneNumber("App.setMenuHiddenIndex", "a row", h.setMenuHiddenIndex),
		"App.setStatusShownIndex": oneNumber("App.setStatusShownIndex", "a row", h.setStatusShownIndex),
		"App.setExplorerEdge":     oneNumber("App.setExplorerEdge", "a row", h.setExplorerEdge),
		"App.setLinksEdge":        oneNumber("App.setLinksEdge", "a row", h.setLinksEdge),
		"App.setRuler":            oneString("App.setRuler", "a number of columns", h.setRuler),

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
