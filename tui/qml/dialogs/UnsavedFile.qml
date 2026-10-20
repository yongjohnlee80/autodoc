// UnsavedFile.qml — the note has unsaved changes: save, discard, or stay. Save takes initial focus:
// Enter saves, and Discard needs its letter or a focus move, so a stray Enter cannot throw work away.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.unsavedFile.unsaved_file")
    Text { wrapMode: Tui.WordWrap; text: App.unsavedQuestion }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.unsavedFile.save");    DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole; onClicked: App.unsaved("save") }
        Button { text: qsTrId("autodoc.dialog.unsavedFile.discard"); DialogButtonBox.buttonRole: DialogButtonBox.DestructiveRole; onClicked: App.unsaved("discard") }
        Button { text: qsTrId("autodoc.dialog.unsavedFile.stay");    DialogButtonBox.buttonRole: DialogButtonBox.RejectRole; onClicked: App.unsaved("stay") }
    }
}
