// TodoNew.qml — a todos keys a: the new task's title; Enter adds it to the store open (todos.go).
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    title: "new task"
    width: 64
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: "title" }
        TextField { id: todoTitle; text: ""; placeholderText: "what is to be done"; onAccepted: App.todoAddTitle(text) }
    }
}
