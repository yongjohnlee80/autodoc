// TodoKeys.qml — a card of keys, as AutoDB's: every command here, by key. GENERATED from
// tui/keys.go (go test ./tui -run TestKeysAreGenerated -update): edit the catalog there, not this
// file. The rows are TEXT, not a list: a list takes the keyboard and eats letters that are keys
// here. With nothing focusable in it, every key reaches the dialog's Shortcuts, one a command, and
// each closes the card before its command runs, so what the command opens is not under it. Esc
// closes it.
Dialog {
    id: todoKeys
    closeOnQ: true
    keyScope: "autodoc.todoKeys"
    title: qsTrId("autodoc.card.todoKeys.title")
    dim: false
    helpText: "↑ ↓ or j k move · q closes the panel · a key runs its command · ?, q or Esc closes"
    Text { text: App.todoKeysText; wrapMode: Tui.WordWrap } // WordWrap keeps its lines: NoWrap is one
    Shortcut { sequence: "?"; onActivated: todoKeys.close() }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoEnter"); onActivated: { todoKeys.close(); App.todoEnter() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoMonthsBack"); onActivated: { todoKeys.close(); App.todoMonthsBack() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoToggleDetail"); onActivated: { todoKeys.close(); App.todoToggleDetail() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoTabBefore"); onActivated: { todoKeys.close(); App.todoTabBefore() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoTabAfter"); onActivated: { todoKeys.close(); App.todoTabAfter() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todosTab.0"); onActivated: { todoKeys.close(); App.todosTab(0) } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todosTab.1"); onActivated: { todoKeys.close(); App.todosTab(1) } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todosTab.2"); onActivated: { todoKeys.close(); App.todosTab(2) } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todosTab.3"); onActivated: { todoKeys.close(); App.todosTab(3) } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todosTab.4"); onActivated: { todoKeys.close(); App.todosTab(4) } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todosTab.5"); onActivated: { todoKeys.close(); App.todosTab(5) } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoChooseStatus"); onActivated: { todoKeys.close(); App.todoChooseStatus() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoAdd"); onActivated: { todoKeys.close(); App.todoAdd() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoRemove"); onActivated: { todoKeys.close(); App.todoRemove() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoAssign"); onActivated: { todoKeys.close(); App.todoAssign() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoVarEdit"); onActivated: { todoKeys.close(); App.todoVarEdit() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoVarMachine"); onActivated: { todoKeys.close(); App.todoVarMachine() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoFocusStore"); onActivated: { todoKeys.close(); App.todoFocusStore() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoNew"); onActivated: { todoKeys.close(); App.todoNew() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoLoad"); onActivated: { todoKeys.close(); App.todoLoad() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoLabel"); onActivated: { todoKeys.close(); App.todoLabel() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoRefresh"); onActivated: { todoKeys.close(); App.todoRefresh() } }
    Shortcut { sequence: keyId("autodoc.todoKeys.todoMove"); onActivated: { todoKeys.close(); App.todoMove() } }
}
