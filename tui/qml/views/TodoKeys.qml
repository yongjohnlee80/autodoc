// TodoKeys.qml — a card of keys, as AutoDB's: every command here, by key. GENERATED from
// tui/keys.go (go test ./tui -run TestKeysAreGenerated -update): edit the catalog there, not this
// file. The rows are TEXT, not a list: a list takes the keyboard and eats letters that are keys
// here. With nothing focusable in it, every key reaches the dialog's Shortcuts, one a command, and
// each closes the card before its command runs, so what the command opens is not under it. Esc
// closes it.
Dialog {
    id: todoKeys
    closeOnQ: true
    title: "todos — keys"
    dim: false
    helpText: "↑ ↓ or j k move · q closes the panel · a key runs its command · ?, q or Esc closes"
    Text { text: App.todoKeysText; wrapMode: Tui.WordWrap } // WordWrap keeps its lines: NoWrap is one
    Shortcut { sequence: "?"; onActivated: todoKeys.close() }
    Shortcut { sequence: "Return"; onActivated: { todoKeys.close(); App.todoOpen() } }
    Shortcut { sequence: "1"; onActivated: { todoKeys.close(); App.todosTab(0) } }
    Shortcut { sequence: "2"; onActivated: { todoKeys.close(); App.todosTab(1) } }
    Shortcut { sequence: "3"; onActivated: { todoKeys.close(); App.todosTab(2) } }
    Shortcut { sequence: "4"; onActivated: { todoKeys.close(); App.todosTab(3) } }
    Shortcut { sequence: "5"; onActivated: { todoKeys.close(); App.todosTab(4) } }
    Shortcut { sequence: "i"; onActivated: { todoKeys.close(); App.todoToggleDetail() } }
    Shortcut { sequence: "o"; onActivated: { todoKeys.close(); App.todoToggleDetail() } }
    Shortcut { sequence: "a"; onActivated: { todoKeys.close(); App.todoAdd() } }
    Shortcut { sequence: "d"; onActivated: { todoKeys.close(); App.todoRemove() } }
    Shortcut { sequence: "Shift+A"; onActivated: { todoKeys.close(); App.todoAssign() } }
    Shortcut { sequence: "s"; onActivated: { todoKeys.close(); App.todoChooseStatus() } }
    Shortcut { sequence: "Shift+R"; onActivated: { todoKeys.close(); App.todoRefresh() } }
    Shortcut { sequence: "Shift+L"; onActivated: { todoKeys.close(); App.todoLabel() } }
    Shortcut { sequence: "Shift+M"; onActivated: { todoKeys.close(); App.todoMove() } }
}
