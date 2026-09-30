// Leader.qml — SPC's card, as AutoDB's: every command here, by key. The rows are TEXT, not a
// list: a list takes the keyboard and eats letters that are keys here. With nothing focusable in
// it, every key reaches the dialog's Shortcuts, one a command, and each closes the card before
// its command runs, so what the command opens is not under it. Esc closes it.
Dialog {
    id: leader
    title: "SPC — commands"
    dim: false
    helpText: "a key runs its command · Esc closes"
    Text { text: App.leaderText; wrapMode: Tui.WordWrap } // WordWrap keeps its lines: NoWrap is one
    Shortcut { sequence: "/"; onActivated: { leader.close(); App.openSearch() } }
    Shortcut { sequence: "Space"; onActivated: { leader.close(); App.openSearch() } }
    Shortcut { sequence: "o"; onActivated: { leader.close(); App.openPicker() } }
    Shortcut { sequence: "n"; onActivated: { leader.close(); App.newNote() } }
    Shortcut { sequence: "s"; onActivated: { leader.close(); App.save() } }
    Shortcut { sequence: "e"; onActivated: { leader.close(); App.toggleExplorer() } }
    Shortcut { sequence: "l"; onActivated: { leader.close(); App.toggleLinks() } }
    Shortcut { sequence: "t"; onActivated: { leader.close(); App.toggleStatusLine() } }
    Shortcut { sequence: "m"; onActivated: { leader.close(); App.toggleMenuBar() } }
    Shortcut { sequence: "w"; onActivated: { leader.close(); App.pickWorkspace() } }
    Shortcut { sequence: "Shift+W"; onActivated: { leader.close(); App.manageWorkspaces() } }
    Shortcut { sequence: ","; onActivated: { leader.close(); App.openPrefs() } }
    Shortcut { sequence: "a"; onActivated: { leader.close(); App.openAIModels() } }
    Shortcut { sequence: "k"; onActivated: { leader.close(); App.toggleKeymap() } }
    Shortcut { sequence: "?"; onActivated: { leader.close(); help.open() } }
    Shortcut { sequence: "h"; onActivated: { leader.close(); App.openNotices() } }
    Shortcut { sequence: "Shift+A"; onActivated: { leader.close(); about.open() } }
    Shortcut { sequence: "Shift+Q"; onActivated: { leader.close(); App.quit() } }
}
