// OpenAnyFile.qml — File › Open file… (SPC f): any file on this machine's disk, in the file
// dialog with a preview. One inside a workspace's root opens as that workspace's file; any other
// opens outside every workspace (outside.go). Open document… (Ctrl+O) is the workspace's picker.
FileDialog {
    title: "open a file"
    fileMode: Tui.OpenFile
    currentFolder: App.browseFolder
    dim: false
    onAccepted: App.openAbsolute(selectedFile)
}
