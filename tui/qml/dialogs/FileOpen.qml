// FileOpen.qml — File › Open document… (Ctrl+O, SPC o): the picker's layout. On the left a filter over the
// workspace's notes, then the notes; on the right the note under the cursor. Enter opens it; q
// closes, outside the field. Ctrl+h/j/k/l move between the parts.
Dialog {
    closeOnQ: true
    maxWidthPercent: 92
    maxHeightPercent: 90
    title: "open a document"
    dim: false
    onOpened: filter.clear()
    Split {
        orientation: Tui.Horizontal
        ratio: 0.42
        Flex {
            direction: Tui.Vertical
            Text { text: "filter: a part of the path" }
            TextField {
                id: filter
                onTextEdited: App.pickerFilter(text)
                onAccepted: App.pickerSelect(notes.currentIndex)
            }
            Frame {
                title: App.pickerStatus
                Layout.fillHeight: true
                TableView {
                    id: notes
                    palette.highlight: Theme.document.highlight
                    palette.highlightedText: Theme.document.highlightedText
                    model: App.pickerRows
                    onCurrentIndexChanged: App.previewPick(index)
                    onActivated: App.pickerSelect(index)
                    TableViewColumn { role: "path"; title: "FILE"; width: 0; elideMode: Tui.ElidePath }
                }
            }
        }
        Frame {
            title: App.openPreviewTitle
            Editor {
                id: openPreview
                readOnly: true
                text: App.openPreviewText
                cursorPosition: App.openPreviewAt
                SyntaxHighlighter { definition: "Markdown" }
            }
        }
    }
    Shortcut { sequence: "Ctrl+K"; onActivated: filter.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+J"; onActivated: notes.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+L"; onActivated: openPreview.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+H"; onActivated: notes.forceActiveFocus() }
    DialogButtonBox {
        Button { text: "&Open"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.pickerSelect(notes.currentIndex)
}
