// NoteConflict.qml — the note changed on disk since it was opened: keep editing, reload the disk's
// version (yours is lost), or overwrite it with yours. Keep takes initial focus, so Enter can
// neither overwrite the disk nor throw your edits away.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "note changed on disk"
    Text { wrapMode: Tui.WordWrap; text: App.conflictQuestion }
    DialogButtonBox {
        Button { text: "&Keep";      DialogButtonBox.buttonRole: DialogButtonBox.RejectRole; onClicked: App.conflict("keep") }
        Button { text: "&Reload";    DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.conflict("reload") }
        Button { text: "&Overwrite"; DialogButtonBox.buttonRole: DialogButtonBox.DestructiveRole; onClicked: App.conflict("overwrite") }
    }
}
