// TodoLabel.qml — the todos panel's L: the store's label, AutoDoc's own name for it (kept as a
// preference, never in the store's files); Enter keeps it, and an empty one shows the name derived
// from its folder again (todos.go).
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    title: "label the store"
    width: 64
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: "label (empty: its folder's name)" }
        TextField { id: todoLabelField; text: App.todoLabelText; placeholderText: "what to call it"; onAccepted: App.todoLabelSet(text) }
    }
}
