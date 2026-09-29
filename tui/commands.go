package tui

import (
	"fmt"
	"strconv"

	"github.com/yongjohnlee80/golib/decl"
	"github.com/yongjohnlee80/golib/parse/qml"
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
		"App.openPicker":    none(h.openPicker),
		"App.pickerFilter":  oneString("App.pickerFilter", "a filter", h.pickerFilter),
		"App.pickerSelect":  oneNumber("App.pickerSelect", "a row", h.pickerSelect),
		"App.openResult":    oneNumber("App.openResult", "a row", h.openResult),
		"App.openBacklink":  oneNumber("App.openBacklink", "a row", h.openBacklink),
		"App.search":        oneString("App.search", "a query", h.search),
		"App.listNotes":     none(h.listNotes),
		"App.pickWorkspace": none(h.pickWorkspace),
		"App.useWorkspace":  oneNumber("App.useWorkspace", "a row", h.useWorkspace),

		"App.manageWorkspaces":         none(h.manageWorkspaces),
		"App.startAddWorkspace":        none(h.startAddWorkspace),
		"App.addWorkspace":             twoStrings("App.addWorkspace", "a name and a root", h.addWorkspace),
		"App.startRenameWorkspace":     oneNumber("App.startRenameWorkspace", "a row", h.startRenameWorkspace),
		"App.renameWorkspace":          oneString("App.renameWorkspace", "a name", h.renameWorkspace),
		"App.startRemoveWorkspace":     oneNumber("App.startRemoveWorkspace", "a row", h.startRemoveWorkspace),
		"App.removeWorkspaceConfirmed": none(h.removeWorkspaceConfirmed),
		"App.unsaved":                  oneString("App.unsaved", "save, discard or stay", h.unsaved),
		"App.conflict":                 oneString("App.conflict", "keep, reload or overwrite", h.conflict),
		"App.edited":                   none(h.edited),
		"App.syncMode":                 none(h.syncMode),
		"App.quit":                     none(h.quit),
		"App.quitConfirmed":            none(func() { h.p.Quit() }),
		"App.useTheme":                 oneString("App.useTheme", "a theme's name", h.useTheme),
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
