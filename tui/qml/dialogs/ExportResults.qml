// ExportResults.qml — the results' Export loaded rows… (results.export; results.csv opens it on CSV):
// what the export takes, the rows loaded in the view as it shows them, said before any file is
// chosen, and its format. The file is chosen next (ExportResultsFile.qml); the export fetches
// nothing (plugintable_export.go).
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    title: qsTrId("autodoc.dialog.exportResults.export_the_results")
    width: 72
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: App.exportHeading }
        Text { text: App.exportMore; wrapMode: Tui.WordWrap; visible: App.exportMoreShown }
        Text { text: " " }
        Text { text: qsTrId("autodoc.dialog.exportResults.format") }
        ComboBox { model: App.exportFormats; textRole: "label"; currentIndex: App.exportFormatIndex; onActivated: App.exportFormatChosen(index) }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.exportResults.cancel"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: qsTrId("autodoc.dialog.exportResults.choose_the_file"); enabled: App.exportCanSave; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
    onAccepted: App.chooseExportFile()
}
