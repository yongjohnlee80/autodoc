// UnsavedNote.qml — the note has unsaved changes: save, discard, or stay. Save takes initial focus:
// Enter saves, and Discard needs its letter or a focus move, so a stray Enter cannot throw work away.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "unsaved note"
    Text { wrapMode: Tui.WordWrap; text: App.unsavedQuestion }
    DialogButtonBox {
        Button { text: "&Save";    DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole; onClicked: App.unsaved("save") }
        Button { text: "&Discard"; DialogButtonBox.buttonRole: DialogButtonBox.DestructiveRole; onClicked: App.unsaved("discard") }
        Button { text: "S&tay";    DialogButtonBox.buttonRole: DialogButtonBox.RejectRole; onClicked: App.unsaved("stay") }
    }
}
