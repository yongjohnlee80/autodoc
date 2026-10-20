// AgentsCard.qml — a card of keys, as AutoDB's: every command here, by key. GENERATED from
// tui/keys.go (go test ./tui -run TestKeysAreGenerated -update): edit the catalog there, not this
// file. The rows are TEXT, not a list: a list takes the keyboard and eats letters that are keys
// here. With nothing focusable in it, every key reaches the dialog's Shortcuts, one a command, and
// each closes the card before its command runs, so what the command opens is not under it. Esc
// closes it.
Dialog {
    id: agentsCard
    closeOnQ: true
    keyScope: "autodoc.agentsCard"
    title: qsTrId("autodoc.card.agentsCard.title")
    dim: false
    helpText: "a key runs its command · Backspace goes back · q or Esc closes"
    Text { text: App.agentsCardText; wrapMode: Tui.WordWrap } // WordWrap keeps its lines: NoWrap is one
    Shortcut { sequence: "Backspace"; onActivated: { agentsCard.close(); leader.open() } }
    Shortcut { sequence: keyId("autodoc.agentsCard.run.lua.agent-buffer"); onActivated: { agentsCard.close(); App.run("lua.agent-buffer") } }
    Shortcut { sequence: keyId("autodoc.agentsCard.run.lua.agent-forward"); onActivated: { agentsCard.close(); App.run("lua.agent-forward") } }
    Shortcut { sequence: keyId("autodoc.agentsCard.toggleAgent"); onActivated: { agentsCard.close(); App.toggleAgent() } }
    Shortcut { sequence: keyId("autodoc.agentsCard.openAgentProfiles"); onActivated: { agentsCard.close(); App.openAgentProfiles() } }
}
