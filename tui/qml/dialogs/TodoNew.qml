// TodoNew.qml — a todos keys a: the new task's title; Enter adds it to the store open (todos.go).
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    title: qsTrId("autodoc.dialog.todoNew.new_task")
    width: 64
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: qsTrId("autodoc.dialog.todoNew.title") }
        TextField { id: todoTitle; text: ""; placeholderText: qsTrId("autodoc.dialog.todoNew.what_is_to_be_done"); onAccepted: App.todoAddTitle(text) }
    }
}
