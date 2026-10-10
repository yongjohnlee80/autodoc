// PluginKeys.qml — SPC p's card: every plugin command with its letter (ADR 1791268009 §2.5). The
// letters are the plugins', bound by the host (plugincmds.go), so its Shortcuts come from a model:
// a Repeater expands them in the Dialog's place before anything is built. As the leader's card,
// it holds nothing focusable, so every letter reaches a Shortcut; each closes the card before its
// command runs. Esc closes it.
Dialog {
    id: pluginKeys
    closeOnQ: true
    title: "SPC p — plugin commands"
    dim: false
    helpText: "a key runs its command · q or Esc closes"
    Text { text: App.pluginKeysText; wrapMode: Tui.WordWrap }
    Shortcut { sequence: "q"; onActivated: pluginKeys.close() }
    Shortcut { sequence: "a"; onActivated: App.pluginKey("a") }
    Shortcut { sequence: "b"; onActivated: App.pluginKey("b") }
    Shortcut { sequence: "c"; onActivated: App.pluginKey("c") }
    Shortcut { sequence: "d"; onActivated: App.pluginKey("d") }
    Shortcut { sequence: "e"; onActivated: App.pluginKey("e") }
    Shortcut { sequence: "f"; onActivated: App.pluginKey("f") }
    Shortcut { sequence: "g"; onActivated: App.pluginKey("g") }
    Shortcut { sequence: "h"; onActivated: App.pluginKey("h") }
    Shortcut { sequence: "i"; onActivated: App.pluginKey("i") }
    Shortcut { sequence: "j"; onActivated: App.pluginKey("j") }
    Shortcut { sequence: "k"; onActivated: App.pluginKey("k") }
    Shortcut { sequence: "l"; onActivated: App.pluginKey("l") }
    Shortcut { sequence: "m"; onActivated: App.pluginKey("m") }
    Shortcut { sequence: "n"; onActivated: App.pluginKey("n") }
    Shortcut { sequence: "o"; onActivated: App.pluginKey("o") }
    Shortcut { sequence: "p"; onActivated: App.pluginKey("p") }
    Shortcut { sequence: "r"; onActivated: App.pluginKey("r") }
    Shortcut { sequence: "s"; onActivated: App.pluginKey("s") }
    Shortcut { sequence: "t"; onActivated: App.pluginKey("t") }
    Shortcut { sequence: "u"; onActivated: App.pluginKey("u") }
    Shortcut { sequence: "v"; onActivated: App.pluginKey("v") }
    Shortcut { sequence: "w"; onActivated: App.pluginKey("w") }
    Shortcut { sequence: "x"; onActivated: App.pluginKey("x") }
    Shortcut { sequence: "y"; onActivated: App.pluginKey("y") }
    Shortcut { sequence: "z"; onActivated: App.pluginKey("z") }
    Shortcut { sequence: "0"; onActivated: App.pluginKey("0") }
    Shortcut { sequence: "1"; onActivated: App.pluginKey("1") }
    Shortcut { sequence: "2"; onActivated: App.pluginKey("2") }
    Shortcut { sequence: "3"; onActivated: App.pluginKey("3") }
    Shortcut { sequence: "4"; onActivated: App.pluginKey("4") }
    Shortcut { sequence: "5"; onActivated: App.pluginKey("5") }
    Shortcut { sequence: "6"; onActivated: App.pluginKey("6") }
    Shortcut { sequence: "7"; onActivated: App.pluginKey("7") }
    Shortcut { sequence: "8"; onActivated: App.pluginKey("8") }
    Shortcut { sequence: "9"; onActivated: App.pluginKey("9") }
}
