// ExportResults.qml — the results' Export all rows as CSV… (results.csv): where every row of the
// results panel's table goes, fetched to the last (plugintable.go). A file there already is asked
// about first, and replaced only once every row is written.
FileDialog {
    title: "export all rows as CSV"
    fileMode: Tui.SaveFile
    currentFolder: App.browseFolder
    dim: false
    onAccepted: App.exportResults(selectedFile)
}
