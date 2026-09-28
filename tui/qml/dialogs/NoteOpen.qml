// NoteOpen.qml — File › Open note: a filter over the workspace's notes.
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "open a note"
    dim: false
    standardButtons: Dialog.Close
    helpText: App.pickerStatus
    onOpened: filter.clear()
    Flex {
        direction: Tui.Vertical
        TextField { id: filter; placeholderText: "filter by path"; onTextEdited: App.pickerFilter(filter.text) }
        TableView {
            palette.highlight: Theme.document.highlight
            palette.highlightedText: Theme.document.highlightedText
            model: App.pickerRows
            Layout.fillHeight: true
            onActivated: App.pickerSelect(index)
            TableViewColumn { role: "path"; title: "NOTE"; width: 0 }
        }
    }
}
