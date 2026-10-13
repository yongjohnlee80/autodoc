// TodoAssign.qml — the todos panel's A, after the agent is chosen: the Instructions: line handed over
// with the task. Enter assigns it (empty: "Task assigned to you."); Esc cancels, and nothing is
// written (todos_assign.go).
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    title: App.todoAssignTitle
    width: 64
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: "Instructions:" }
        TextField { id: todoAssignField; text: App.todoAssignText; placeholderText: "Task assigned to you."; onAccepted: App.todoAssignSet(text) }
    }
}
