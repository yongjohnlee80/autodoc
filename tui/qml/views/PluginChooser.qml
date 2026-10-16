// PluginChooser.qml — SPC p (ADR 1792132889): the plugins with commands, each with the letter the user
// gave it, the host's cursor (›) on one. A plugin's letter, or Enter, shows its card (PluginKeys.qml);
// j/k move; * asks for a letter for the plugin under the cursor, and the next key is it (Backspace
// removes it). It is a card, as the leader's is: text, with nothing focusable in it, so every key
// reaches a Shortcut, and each is the host's to read (pluginpick.go). A focused list would take j, k,
// g, G and Enter before them. Esc closes it.
Dialog {
    id: pluginChooser
    title: "SPC p — plugins"
    dim: false
    helpText: App.pluginChooserHelp
    Text { text: App.pluginChooserText; wrapMode: Tui.WordWrap }
    Shortcut { sequence: "Up"; onActivated: App.pluginChooserKey("Up") }
    Shortcut { sequence: "Down"; onActivated: App.pluginChooserKey("Down") }
    Shortcut { sequence: "Enter"; onActivated: App.pluginChooserKey("Enter") }
    Shortcut { sequence: "Backspace"; onActivated: App.pluginChooserKey("Backspace") }
    // * as the terminal sends it, bare or with the Shift it took
    Shortcut { sequence: "*"; onActivated: App.pluginChooserKey("*") }
    Shortcut { sequence: "Shift+*"; onActivated: App.pluginChooserKey("*") }
    Shortcut { sequence: "a"; onActivated: App.pluginChooserKey("a") }
    Shortcut { sequence: "b"; onActivated: App.pluginChooserKey("b") }
    Shortcut { sequence: "c"; onActivated: App.pluginChooserKey("c") }
    Shortcut { sequence: "d"; onActivated: App.pluginChooserKey("d") }
    Shortcut { sequence: "e"; onActivated: App.pluginChooserKey("e") }
    Shortcut { sequence: "f"; onActivated: App.pluginChooserKey("f") }
    Shortcut { sequence: "g"; onActivated: App.pluginChooserKey("g") }
    Shortcut { sequence: "h"; onActivated: App.pluginChooserKey("h") }
    Shortcut { sequence: "i"; onActivated: App.pluginChooserKey("i") }
    Shortcut { sequence: "j"; onActivated: App.pluginChooserKey("j") }
    Shortcut { sequence: "k"; onActivated: App.pluginChooserKey("k") }
    Shortcut { sequence: "l"; onActivated: App.pluginChooserKey("l") }
    Shortcut { sequence: "m"; onActivated: App.pluginChooserKey("m") }
    Shortcut { sequence: "n"; onActivated: App.pluginChooserKey("n") }
    Shortcut { sequence: "o"; onActivated: App.pluginChooserKey("o") }
    Shortcut { sequence: "p"; onActivated: App.pluginChooserKey("p") }
    Shortcut { sequence: "q"; onActivated: App.pluginChooserKey("q") }
    Shortcut { sequence: "r"; onActivated: App.pluginChooserKey("r") }
    Shortcut { sequence: "s"; onActivated: App.pluginChooserKey("s") }
    Shortcut { sequence: "t"; onActivated: App.pluginChooserKey("t") }
    Shortcut { sequence: "u"; onActivated: App.pluginChooserKey("u") }
    Shortcut { sequence: "v"; onActivated: App.pluginChooserKey("v") }
    Shortcut { sequence: "w"; onActivated: App.pluginChooserKey("w") }
    Shortcut { sequence: "x"; onActivated: App.pluginChooserKey("x") }
    Shortcut { sequence: "y"; onActivated: App.pluginChooserKey("y") }
    Shortcut { sequence: "z"; onActivated: App.pluginChooserKey("z") }
    Shortcut { sequence: "0"; onActivated: App.pluginChooserKey("0") }
    Shortcut { sequence: "1"; onActivated: App.pluginChooserKey("1") }
    Shortcut { sequence: "2"; onActivated: App.pluginChooserKey("2") }
    Shortcut { sequence: "3"; onActivated: App.pluginChooserKey("3") }
    Shortcut { sequence: "4"; onActivated: App.pluginChooserKey("4") }
    Shortcut { sequence: "5"; onActivated: App.pluginChooserKey("5") }
    Shortcut { sequence: "6"; onActivated: App.pluginChooserKey("6") }
    Shortcut { sequence: "7"; onActivated: App.pluginChooserKey("7") }
    Shortcut { sequence: "8"; onActivated: App.pluginChooserKey("8") }
    Shortcut { sequence: "9"; onActivated: App.pluginChooserKey("9") }
}
