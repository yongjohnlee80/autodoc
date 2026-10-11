// TodoRemove.qml — the todos' d asks before a task's file is deleted.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "remove the task?"
    width: 64
    dim: false
    standardButtons: Dialog.Yes | Dialog.No
    defaultButton: Dialog.No
    Text { wrapMode: Tui.WordWrap; text: App.todoRemoveQuestion }
    onAccepted: App.todoRemoveConfirmed()
}
