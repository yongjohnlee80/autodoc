// FileName.qml — File › New note (Ctrl+N): the picker's layout. On the left the new note's path
// (".md" is added when it has none), then the workspace's notes under it, Enter on one taking its
// folder; on the right the note under the cursor. Create makes the note; a refused path opens it
// again with the reason under the path (App.fileNameError).
Dialog {
    closeOnQ: true
    maxWidthPercent: 92
    maxHeightPercent: 90
    title: "new file"
    dim: false
    Split {
        orientation: Tui.Horizontal
        ratio: 0.42
        Flex {
            direction: Tui.Vertical
            Text { text: "path" }
            TextField {
                id: name
                text: App.newFilePath
                placeholderText: "folder/name.md"
                onTextEdited: App.newFileFilter(text)
                onAccepted: App.createFile(text)
            }
            Text { text: App.fileNameError; wrapMode: Tui.WordWrap }
            Frame {
                title: "the files"
                Layout.fillHeight: true
                TableView {
                    id: existing
                    palette.highlight: Theme.document.highlight
                    palette.highlightedText: Theme.document.highlightedText
                    model: App.newFiles
                    onCurrentIndexChanged: App.previewNew(index)
                    onActivated: App.newFileFolder(index)
                    TableViewColumn { role: "path"; title: "FILE"; width: 0; elideMode: Tui.ElidePath }
                }
            }
        }
        Frame {
            title: App.newPreviewTitle
            Editor {
                id: newPreview
                readOnly: true
                text: App.newPreviewText
                cursorPosition: App.newPreviewAt
                SyntaxHighlighter { definition: "Markdown" }
            }
        }
    }
    Shortcut { sequence: "Ctrl+K"; onActivated: name.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+J"; onActivated: existing.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+L"; onActivated: newPreview.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+H"; onActivated: existing.forceActiveFocus() }
    DialogButtonBox {
        Button { text: "C&reate"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.createFile(name.text)
}
