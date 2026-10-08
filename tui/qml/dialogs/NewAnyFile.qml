// NewAnyFile.qml — File › New file… (Ctrl+N, SPC n): a new file in any folder, named in the save
// dialog (.md added when it has none). One inside a workspace's root is that workspace's file
// (outside.go).
FileDialog {
    title: "new file"
    fileMode: Tui.SaveFile
    currentFolder: App.browseFolder
    dim: false
    onAccepted: App.createAbsolute(selectedFile)
}
