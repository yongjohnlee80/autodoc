// FileName.qml — naming the untitled draft (Ctrl+S on it): the picker over the workspace's notes. On
// the left its path (".md" is added when it has none), then the notes under it, Enter on one taking
// its folder; on the right the note under the cursor. Create makes the note at the path typed; a
// refused path opens it again with the reason under the path (App.fileNameError).
Picker {
    name: "new"
    title: "name the draft"
    label: "path"
    fieldText: App.newFilePath
    placeholder: "folder/name.md"
    note: App.fileNameError
    noteShown: true
    model: App.newFiles
    listTitle: "the files"
    previewTitle: App.newPreviewTitle
    previewText: App.newPreviewText
    previewAt: App.newPreviewAt
    acceptText: "C&reate"
    TableViewColumn { role: "path"; title: "FILE"; width: 0; elideMode: Tui.ElidePath }
}
