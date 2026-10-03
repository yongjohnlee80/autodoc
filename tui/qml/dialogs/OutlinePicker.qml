// OutlinePicker.qml — Go › Outline (SPC c): the picker's layout over the note's headings, as the
// editor's text has them, saved or not. On the left a filter, then the headings, indented by
// level; on the right the note at the heading under the cursor. Enter jumps there; q closes,
// outside the field. Ctrl+h/j/k/l move between the parts.
Dialog {
    closeOnQ: true
    maxWidthPercent: 92
    maxHeightPercent: 90
    title: "outline"
    dim: false
    onOpened: outlineFilter.clear()
    Split {
        orientation: Tui.Horizontal
        ratio: 0.42
        Flex {
            direction: Tui.Vertical
            Text { text: "filter: a part of a heading" }
            TextField {
                id: outlineFilter
                onTextEdited: App.outlineFilter(text)
                onAccepted: App.jumpToHeading(headings.currentIndex)
            }
            Frame {
                title: App.outlineStatus
                Layout.fillHeight: true
                TableView {
                    id: headings
                    palette.highlight: Theme.document.highlight
                    palette.highlightedText: Theme.document.highlightedText
                    model: App.outlineRows
                    onCurrentIndexChanged: App.previewHeading(index)
                    onActivated: App.jumpToHeading(index)
                    TableViewColumn { role: "heading"; title: "HEADING"; width: 0 }
                    TableViewColumn { role: "line"; title: "LINE"; width: 6 }
                }
            }
        }
        Frame {
            title: App.outlinePreviewTitle
            Editor {
                id: outlinePreview
                readOnly: true
                text: App.outlinePreviewText
                cursorPosition: App.outlinePreviewAt
                SyntaxHighlighter { definition: "Markdown" }
            }
        }
    }
    Shortcut { sequence: "Ctrl+K"; onActivated: outlineFilter.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+J"; onActivated: headings.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+L"; onActivated: outlinePreview.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+H"; onActivated: headings.forceActiveFocus() }
    DialogButtonBox {
        Button { text: "&Jump"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.jumpToHeading(headings.currentIndex)
}
