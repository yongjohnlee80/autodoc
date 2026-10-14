// LuaKeys.qml — SPC u's card: every Lua leader key with its letter (ADR 1792004705 §4.5). The
// letters are the scripts', bound by autodoc.map as they load (luakeys.go), so the card has a
// Shortcut for every letter it may hold and App.luaKey runs the one bound, if any. As the
// leader's card, it holds nothing focusable, so every letter reaches a Shortcut; each closes the
// card before its command runs. q or Esc closes it.
Dialog {
    id: luaKeys
    closeOnQ: true
    title: "SPC u — your Lua keys"
    dim: false
    helpText: "a key runs its command · q or Esc closes"
    Text { text: App.luaKeysText; wrapMode: Tui.WordWrap }
    Shortcut { sequence: "q"; onActivated: luaKeys.close() }
    Shortcut { sequence: "a"; onActivated: App.luaKey("a") }
    Shortcut { sequence: "b"; onActivated: App.luaKey("b") }
    Shortcut { sequence: "c"; onActivated: App.luaKey("c") }
    Shortcut { sequence: "d"; onActivated: App.luaKey("d") }
    Shortcut { sequence: "e"; onActivated: App.luaKey("e") }
    Shortcut { sequence: "f"; onActivated: App.luaKey("f") }
    Shortcut { sequence: "g"; onActivated: App.luaKey("g") }
    Shortcut { sequence: "h"; onActivated: App.luaKey("h") }
    Shortcut { sequence: "i"; onActivated: App.luaKey("i") }
    Shortcut { sequence: "j"; onActivated: App.luaKey("j") }
    Shortcut { sequence: "k"; onActivated: App.luaKey("k") }
    Shortcut { sequence: "l"; onActivated: App.luaKey("l") }
    Shortcut { sequence: "m"; onActivated: App.luaKey("m") }
    Shortcut { sequence: "n"; onActivated: App.luaKey("n") }
    Shortcut { sequence: "o"; onActivated: App.luaKey("o") }
    Shortcut { sequence: "p"; onActivated: App.luaKey("p") }
    Shortcut { sequence: "r"; onActivated: App.luaKey("r") }
    Shortcut { sequence: "s"; onActivated: App.luaKey("s") }
    Shortcut { sequence: "t"; onActivated: App.luaKey("t") }
    Shortcut { sequence: "u"; onActivated: App.luaKey("u") }
    Shortcut { sequence: "v"; onActivated: App.luaKey("v") }
    Shortcut { sequence: "w"; onActivated: App.luaKey("w") }
    Shortcut { sequence: "x"; onActivated: App.luaKey("x") }
    Shortcut { sequence: "y"; onActivated: App.luaKey("y") }
    Shortcut { sequence: "z"; onActivated: App.luaKey("z") }
    Shortcut { sequence: "0"; onActivated: App.luaKey("0") }
    Shortcut { sequence: "1"; onActivated: App.luaKey("1") }
    Shortcut { sequence: "2"; onActivated: App.luaKey("2") }
    Shortcut { sequence: "3"; onActivated: App.luaKey("3") }
    Shortcut { sequence: "4"; onActivated: App.luaKey("4") }
    Shortcut { sequence: "5"; onActivated: App.luaKey("5") }
    Shortcut { sequence: "6"; onActivated: App.luaKey("6") }
    Shortcut { sequence: "7"; onActivated: App.luaKey("7") }
    Shortcut { sequence: "8"; onActivated: App.luaKey("8") }
    Shortcut { sequence: "9"; onActivated: App.luaKey("9") }
}
