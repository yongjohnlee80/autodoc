package plugin

import "github.com/yongjohnlee80/golib/tui"

// keyNames are the protocol's names for the keys that have one (Key.Key), by golib's tui key
// code. The host names the keys it sends by them (KeyName), and plugin/decl gives a key it is sent
// back its code by the same table (KeyCode), so the two can't disagree.
var keyNames = map[rune]string{
	tui.KeyUp: "Up", tui.KeyDown: "Down", tui.KeyLeft: "Left", tui.KeyRight: "Right",
	tui.KeyEnter: "Enter", tui.KeyTab: "Tab", tui.KeyBackspace: "Backspace",
	tui.KeyDelete: "Delete", tui.KeyInsert: "Insert", tui.KeyHome: "Home", tui.KeyEnd: "End",
	tui.KeyPageUp: "PageUp", tui.KeyPageDown: "PageDown",
	tui.KeyF1: "F1", tui.KeyF2: "F2", tui.KeyF3: "F3", tui.KeyF4: "F4", tui.KeyF5: "F5",
	tui.KeyF6: "F6", tui.KeyF7: "F7", tui.KeyF8: "F8", tui.KeyF9: "F9", tui.KeyF10: "F10",
	tui.KeyF11: "F11", tui.KeyF12: "F12",
}

var keyCodes = func() map[string]rune {
	m := make(map[string]rune, len(keyNames))
	for code, name := range keyNames {
		m[name] = code
	}
	return m
}()

// KeyName is the protocol's name for the golib tui key code ("Up", "F5"); "" for a key with no
// name of its own (a printable key is sent as its text).
func KeyName(code rune) string { return keyNames[code] }

// KeyCode is the golib tui key code of a key the protocol names ("Up", "F5"); false for a name it
// does not have.
func KeyCode(name string) (rune, bool) {
	code, ok := keyCodes[name]
	return code, ok
}
