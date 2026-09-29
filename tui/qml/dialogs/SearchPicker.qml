// SearchPicker.qml — Go › Search (Ctrl+G, /): the picker's layout. On the left the search, then its
// hits, a section a row; on the right the note under the cursor, at that section, the search's
// words marked. Enter opens the hit, the cursor at its section; q closes, outside the field.
// Ctrl+h/j/k/l move between the parts.
Dialog {
    closeOnQ: true
    maxWidthPercent: 92
    maxHeightPercent: 90
    title: "search"
    dim: false
    Split {
        orientation: Tui.Horizontal
        ratio: 0.42
        Flex {
            direction: Tui.Vertical
            Text { text: "search: words; a * ends a prefix" }
            TextField {
                id: query
                onTextEdited: App.searchLive(text)
                onAccepted: App.openHit(hits.currentIndex)
            }
            Frame {
                title: App.hitsTitle
                Layout.fillHeight: true
                TableView {
                    id: hits
                    palette.highlight: Theme.document.highlight
                    palette.highlightedText: Theme.document.highlightedText
                    model: App.hits
                    onCurrentIndexChanged: App.previewHit(index)
                    onActivated: App.openHit(index)
                    TableViewColumn { role: "path"; title: "NOTE"; width: 0; elideMode: Tui.ElidePath }
                    TableViewColumn { role: "section"; title: "SECTION"; width: 22 }
                }
            }
        }
        Frame {
            title: App.searchPreviewTitle
            Editor {
                id: searchPreview
                readOnly: true
                text: App.searchPreviewText
                cursorPosition: App.searchPreviewAt
                SyntaxHighlighter { definition: "Markdown (search)" }
            }
        }
    }
    Shortcut { sequence: "Ctrl+K"; onActivated: query.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+J"; onActivated: hits.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+L"; onActivated: searchPreview.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+H"; onActivated: hits.forceActiveFocus() }
    DialogButtonBox {
        Button { text: "&Open"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.openHit(hits.currentIndex)
}
