// ExportResults.qml — the results' Export loaded rows… (results.export; results.csv opens it on CSV):
// what the export takes, the rows loaded in the view as it shows them, said before any file is
// chosen, and its format. The file is chosen next (ExportResultsFile.qml); the export fetches
// nothing (plugintable_export.go).
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    title: "export the results"
    width: 72
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: App.exportHeading }
        Text { text: App.exportMore; wrapMode: Tui.WordWrap; visible: App.exportMoreShown }
        Text { text: " " }
        Text { text: "format" }
        ComboBox { model: App.exportFormats; textRole: "label"; currentIndex: App.exportFormatIndex; onActivated: App.exportFormatChosen(index) }
    }
    DialogButtonBox {
        Button { text: "&Cancel"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: "Choose the &file…"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
    onAccepted: App.chooseExportFile()
}
