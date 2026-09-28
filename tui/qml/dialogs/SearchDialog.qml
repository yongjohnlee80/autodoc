// SearchDialog.qml — search the workspace; the hits replace the notes pane.
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "search"
    dim: false
    width: 60
    standardButtons: Dialog.Ok | Dialog.Cancel
    defaultButton: Dialog.Ok
    helpText: App.searchHelp
    TextField { id: query; text: App.lastQuery; placeholderText: "words; a * ends a prefix" }
    onAccepted: App.search(query.text)
}
