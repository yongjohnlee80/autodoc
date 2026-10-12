// Picker.qml — the pickers' one layout: on the left a field over a list, on the right the item
// under the list's cursor, the buttons beneath. A use names its picker (name, a pick in Go) and
// gives its words, its list and its preview; its TableViewColumns go into the list. Every handler
// calls an App.pick* command with the name: the field's edits filter, the list's cursor previews,
// Enter on a row chooses it, accept (the button, or Enter in the field) takes the row under the
// cursor or the field's text, as the picker says. q closes, outside the field; Ctrl+h/j/k/l move
// between the parts.
Dialog {
    required property string name
    required property var model
    property string label: "filter"
    property string fieldText: ""
    property string placeholder: ""
    property string note: ""
    property bool noteShown: false
    property string listTitle: ""
    property string previewTitle: ""
    property string previewText: ""
    property int previewAt: 0
    property string acceptText: "&Open"
    default property alias columns: list.data
    closeOnQ: true
    maxWidthPercent: 92
    maxHeightPercent: 90
    dim: false
    Split {
        orientation: Tui.Horizontal
        ratio: 0.42
        Flex {
            direction: Tui.Vertical
            Text { text: label }
            TextField {
                id: field
                text: fieldText
                placeholderText: placeholder
                onTextEdited: App.pickFilter(name, text)
                onAccepted: App.pickAccept(name, list.currentIndex, text)
            }
            Text { text: note; wrapMode: Tui.WordWrap; visible: noteShown }
            Frame {
                title: listTitle
                Layout.fillHeight: true
                TableView {
                    id: list
                    palette.highlight: Theme.document.highlight
                    palette.highlightedText: Theme.document.highlightedText
                    model: model
                    onCurrentIndexChanged: App.pickMove(name, index)
                    onActivated: App.pickChoose(name, index)
                }
            }
        }
        Frame {
            title: previewTitle
            Editor {
                id: preview
                readOnly: true
                text: previewText
                cursorPosition: previewAt
                SyntaxHighlighter { definition: "Markdown" }
            }
        }
    }
    Shortcut { sequence: "Ctrl+K"; onActivated: field.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+J"; onActivated: list.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+L"; onActivated: preview.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+H"; onActivated: list.forceActiveFocus() }
    DialogButtonBox {
        Button { text: acceptText; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.pickAccept(name, list.currentIndex, field.text)
    onRejected: App.pickCancel(name)
}
