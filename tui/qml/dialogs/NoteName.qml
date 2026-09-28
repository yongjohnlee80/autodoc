// NoteName.qml — File › New note: its path in the workspace (".md" is added when it has none). A
// refused name opens it again with the reason on its help line (App.noteNameError).
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "new note"
    width: 64
    standardButtons: Dialog.Ok | Dialog.Cancel
    defaultButton: Dialog.Ok
    helpText: App.noteNameError
    TextField { id: name; placeholderText: "folder/name.md" }
    onOpened: name.clear()
    onAccepted: App.createNote(name.text)
}
