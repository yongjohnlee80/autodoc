// PluginKeys.qml — SPC p's card: every plugin command with its letter (ADR 1791268009 §2.5). The
// letters are the plugins', bound by the host (plugincmds.go), so its Shortcuts come from a model:
// a Repeater expands them in the Dialog's place before anything is built. As the leader's card,
// it holds nothing focusable, so every letter reaches a Shortcut; each closes the card before its
// command runs. Esc closes it.
Dialog {
    id: pluginKeys
    title: "SPC p — plugin commands"
    dim: false
    helpText: "a key runs its command · q or Esc closes"
    Text { text: App.pluginKeysText; wrapMode: Tui.WordWrap }
    Repeater {
        model: App.pluginKeys
        Shortcut { sequence: model.key; onActivated: { pluginKeys.close(); App.pluginEntry(model.target) } }
    }
}
