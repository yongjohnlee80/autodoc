// FileOpen.qml — File › Open document… (SPC o): the picker over the workspace's notes. On the
// left a filter, then the notes; on the right the note under the cursor. Enter opens it.
Picker {
    name: "open"
    title: "open a document"
    label: "filter: a part of the path"
    fieldText: App.openField
    model: App.pickerRows
    listTitle: App.pickerStatus
    previewTitle: App.openPreviewTitle
    previewText: App.openPreviewText
    previewAt: App.openPreviewAt
    acceptText: "&Open"
    TableViewColumn { role: "path"; title: "FILE"; width: 0; elideMode: Tui.ElidePath }
}
