// TodoVar.qml — the todos panel's Variables tab: e, m and a ask for a variable's value here (a:
// NAME=value); Enter keeps it, in the store's file or for this machine as the title says, and an
// empty value clears it (todos_vars.go).
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    title: App.todoVarTitle
    width: 72
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: "value (a relative path is the store's folder's; empty: clear it)" }
        TextField { id: todoVarField; text: App.todoVarText; placeholderText: "/abs/path, ../relative, or ~/path"; onAccepted: App.todoVarSet(text) }
    }
}
