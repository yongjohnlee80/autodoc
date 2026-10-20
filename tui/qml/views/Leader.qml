// Leader.qml — a card of keys, as AutoDB's: every command here, by key. GENERATED from
// tui/keys.go (go test ./tui -run TestKeysAreGenerated -update): edit the catalog there, not this
// file. The rows are TEXT, not a list: a list takes the keyboard and eats letters that are keys
// here. With nothing focusable in it, every key reaches the dialog's Shortcuts, one a command, and
// each closes the card before its command runs, so what the command opens is not under it. Esc
// closes it.
Dialog {
    id: leader
    closeOnQ: true
    keyScope: "autodoc.leader"
    title: qsTrId("autodoc.card.leader.title")
    dim: false
    helpText: "a key runs its command · q or Esc closes"
    Text { text: App.leaderText; wrapMode: Tui.WordWrap } // WordWrap keeps its lines: NoWrap is one
    Shortcut { sequence: keyId("autodoc.leader.openSearch"); onActivated: { leader.close(); App.openSearch() } }
    Shortcut { sequence: keyId("autodoc.leader.openPicker"); onActivated: { leader.close(); App.openPicker() } }
    Shortcut { sequence: keyId("autodoc.leader.openFileDialog"); onActivated: { leader.close(); App.openFileDialog() } }
    Shortcut { sequence: keyId("autodoc.leader.openRecent"); onActivated: { leader.close(); App.openRecent() } }
    Shortcut { sequence: keyId("autodoc.leader.followLink"); onActivated: { leader.close(); App.followLink() } }
    Shortcut { sequence: keyId("autodoc.leader.goBack"); onActivated: { leader.close(); App.goBack() } }
    Shortcut { sequence: keyId("autodoc.leader.openBuffers"); onActivated: { leader.close(); App.openBuffers() } }
    Shortcut { sequence: keyId("autodoc.leader.newFile"); onActivated: { leader.close(); App.newFile() } }
    Shortcut { sequence: keyId("autodoc.leader.closeBuffer"); onActivated: { leader.close(); App.closeBuffer() } }
    Shortcut { sequence: keyId("autodoc.leader.openOutline"); onActivated: { leader.close(); App.openOutline() } }
    Shortcut { sequence: keyId("autodoc.leader.previewDiagram"); onActivated: { leader.close(); App.previewDiagram() } }
    Shortcut { sequence: keyId("autodoc.leader.previewHTML"); onActivated: { leader.close(); App.previewHTML() } }
    Shortcut { sequence: keyId("autodoc.leader.toggleExplorer"); onActivated: { leader.close(); App.toggleExplorer() } }
    Shortcut { sequence: keyId("autodoc.leader.toggleRelations"); onActivated: { leader.close(); App.toggleRelations() } }
    Shortcut { sequence: keyId("autodoc.leader.toggleTerminal"); onActivated: { leader.close(); App.toggleTerminal() } }
    Shortcut { sequence: keyId("autodoc.leader.openTodos"); onActivated: { leader.close(); App.openTodos() } }
    Shortcut { sequence: keyId("autodoc.leader.maximizePanel"); onActivated: { leader.close(); App.maximizePanel() } }
    Shortcut { sequence: keyId("autodoc.leader.openNotices"); onActivated: { leader.close(); App.openNotices() } }
    Shortcut { sequence: keyId("autodoc.leader.toggleAgent"); onActivated: { leader.close(); App.toggleAgent() } }
    Shortcut { sequence: keyId("autodoc.leader.openAgentProfiles"); onActivated: { leader.close(); App.openAgentProfiles() } }
    Shortcut { sequence: keyId("autodoc.leader.help.open"); onActivated: { leader.close(); help.open() } }
    Shortcut { sequence: keyId("autodoc.leader.about.open"); onActivated: { leader.close(); about.open() } }
    Shortcut { sequence: keyId("autodoc.leader.quit"); onActivated: { leader.close(); App.quit() } }
    Shortcut { sequence: keyId("autodoc.leader.pickWorkspace"); onActivated: { leader.close(); App.pickWorkspace() } }
    Shortcut { sequence: keyId("autodoc.leader.manageWorkspaces"); onActivated: { leader.close(); App.manageWorkspaces() } }
    Shortcut { sequence: keyId("autodoc.leader.openAIModels"); onActivated: { leader.close(); App.openAIModels() } }
    Shortcut { sequence: keyId("autodoc.leader.openPrefs"); onActivated: { leader.close(); App.openPrefs() } }
    Shortcut { sequence: keyId("autodoc.leader.toggleKeymap"); onActivated: { leader.close(); App.toggleKeymap() } }
    Shortcut { sequence: keyId("autodoc.leader.leaderMenuBar"); onActivated: { leader.close(); App.leaderMenuBar() } }
    Shortcut { sequence: keyId("autodoc.leader.openWidgets"); onActivated: { leader.close(); App.openWidgets() } }
    Shortcut { sequence: keyId("autodoc.leader.openContextMenu"); onActivated: { leader.close(); App.openContextMenu() } }
    Shortcut { sequence: keyId("autodoc.leader.toggleStatusLine"); onActivated: { leader.close(); App.toggleStatusLine() } }
    Shortcut { sequence: keyId("autodoc.leader.arrangePanel"); onActivated: { leader.close(); App.arrangePanel() } }
    Shortcut { sequence: keyId("autodoc.leader.open.agentsCard"); onActivated: { leader.close(); agentsCard.open() } }
    Shortcut { sequence: keyId("autodoc.leader.openPluginChooser"); onActivated: { leader.close(); App.openPluginChooser() } }
    Shortcut { sequence: keyId("autodoc.leader.luaKeys.open"); onActivated: { leader.close(); luaKeys.open() } }
}
