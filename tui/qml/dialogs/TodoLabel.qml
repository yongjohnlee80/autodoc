// TodoLabel.qml — the todos panel's L: the store's label, AutoDoc's own name for it (kept as a
// preference, never in the store's files); Enter keeps it, and an empty one shows the name derived
// from its folder again (todos.go).
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    title: qsTrId("autodoc.dialog.todoLabel.label_the_store")
    width: 64
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: qsTrId("autodoc.dialog.todoLabel.label_empty_its_folder_s_name") }
        TextField { id: todoLabelField; text: App.todoLabelText; placeholderText: qsTrId("autodoc.dialog.todoLabel.what_to_call_it"); onAccepted: App.todoLabelSet(text) }
    }
}
