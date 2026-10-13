// Leader.qml — a card of keys, as AutoDB's: every command here, by key. GENERATED from
// tui/keys.go (go test ./tui -run TestKeysAreGenerated -update): edit the catalog there, not this
// file. The rows are TEXT, not a list: a list takes the keyboard and eats letters that are keys
// here. With nothing focusable in it, every key reaches the dialog's Shortcuts, one a command, and
// each closes the card before its command runs, so what the command opens is not under it. Esc
// closes it.
Dialog {
    id: leader
    closeOnQ: true
    title: "SPC — commands"
    dim: false
    helpText: "a key runs its command · q or Esc closes"
    Text { text: App.leaderText; wrapMode: Tui.WordWrap } // WordWrap keeps its lines: NoWrap is one
    Shortcut { sequence: "/"; onActivated: { leader.close(); App.openSearch() } }
    Shortcut { sequence: "Space"; onActivated: { leader.close(); App.openSearch() } }
    Shortcut { sequence: "o"; onActivated: { leader.close(); App.openPicker() } }
    Shortcut { sequence: "f"; onActivated: { leader.close(); App.openFileDialog() } }
    Shortcut { sequence: "r"; onActivated: { leader.close(); App.openRecent() } }
    Shortcut { sequence: "j"; onActivated: { leader.close(); App.followLink() } }
    Shortcut { sequence: "b"; onActivated: { leader.close(); App.goBack() } }
    Shortcut { sequence: "n"; onActivated: { leader.close(); App.newFile() } }
    Shortcut { sequence: "Shift+O"; onActivated: { leader.close(); App.openOutline() } }
    Shortcut { sequence: "d"; onActivated: { leader.close(); App.previewDiagram() } }
    Shortcut { sequence: "Shift+H"; onActivated: { leader.close(); App.previewHTML() } }
    Shortcut { sequence: "e"; onActivated: { leader.close(); App.toggleExplorer() } }
    Shortcut { sequence: "l"; onActivated: { leader.close(); App.toggleRelations() } }
    Shortcut { sequence: "`"; onActivated: { leader.close(); App.toggleTerminal() } }
    Shortcut { sequence: "x"; onActivated: { leader.close(); App.openTodos() } }
    Shortcut { sequence: "Shift+M"; onActivated: { leader.close(); App.maximizePanel() } }
    Shortcut { sequence: "h"; onActivated: { leader.close(); App.openNotices() } }
    Shortcut { sequence: "g"; onActivated: { leader.close(); App.toggleAgent() } }
    Shortcut { sequence: "Shift+G"; onActivated: { leader.close(); App.openAgentProfiles() } }
    Shortcut { sequence: "?"; onActivated: { leader.close(); help.open() } }
    Shortcut { sequence: "Shift+A"; onActivated: { leader.close(); about.open() } }
    Shortcut { sequence: "Shift+Q"; onActivated: { leader.close(); App.quit() } }
    Shortcut { sequence: "w"; onActivated: { leader.close(); App.pickWorkspace() } }
    Shortcut { sequence: "Shift+W"; onActivated: { leader.close(); App.manageWorkspaces() } }
    Shortcut { sequence: "s"; onActivated: { leader.close(); App.openAIModels() } }
    Shortcut { sequence: ","; onActivated: { leader.close(); App.openPrefs() } }
    Shortcut { sequence: "k"; onActivated: { leader.close(); App.toggleKeymap() } }
    Shortcut { sequence: "m"; onActivated: { leader.close(); App.leaderMenuBar() } }
    Shortcut { sequence: "."; onActivated: { leader.close(); App.openContextMenu() } }
    Shortcut { sequence: "t"; onActivated: { leader.close(); App.toggleStatusLine() } }
    Shortcut { sequence: "Shift+L"; onActivated: { leader.close(); App.arrangePanel() } }
    Shortcut { sequence: "a"; onActivated: { leader.close(); agentsCard.open() } }
    Shortcut { sequence: "p"; onActivated: { leader.close(); pluginKeys.open() } }
}
