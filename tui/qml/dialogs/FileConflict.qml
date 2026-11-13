// FileConflict.qml — the note changed on disk since it was opened: keep editing, reload the disk's
// version (yours is lost), overwrite it with yours, or (in a terminal) compare the two and merge. Keep takes initial focus, so Enter can
// neither overwrite the disk nor throw your edits away.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.fileConflict.file_changed_on_disk")
    Text { wrapMode: Tui.WordWrap; text: App.conflictQuestion }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.fileConflict.keep");      DialogButtonBox.buttonRole: DialogButtonBox.RejectRole; onClicked: App.conflict("keep") }
        Button { text: qsTrId("autodoc.dialog.fileConflict.reload");    DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.conflict("reload") }
        Button { text: qsTrId("autodoc.dialog.fileConflict.overwrite"); DialogButtonBox.buttonRole: DialogButtonBox.DestructiveRole; onClicked: App.conflict("overwrite") }
        // the two texts merged in a terminal's Compare (compare.go); a window has none yet
        Button { text: qsTrId("autodoc.dialog.fileConflict.compare");   DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; visible: App.canCompare; onClicked: App.conflict("compare") }
    }
}
