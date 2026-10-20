// ExportResultsFile.qml — where the results' export goes (ExportResults.qml chose what it takes and
// its format): a name with no extension takes the format's. A file there already is asked about
// first, and replaced only once every row is written (plugintable_export.go).
FileDialog {
    title: qsTrId("autodoc.dialog.exportResultsFile.export_the_loaded_rows_to")
    fileMode: Tui.SaveFile
    currentFolder: App.browseFolder
    selectedFile: App.exportFile
    dim: false
    onAccepted: App.exportResults(selectedFile)
}
