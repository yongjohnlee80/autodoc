// Recovery.qml — unsaved work AutoDoc kept when it last ended without being asked to (a signal, the
// terminal closing): restore it, discard the copy, or ask again at the next start. Restore takes
// initial focus; Discard needs its letter, so a stray Enter cannot throw the copy away.
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.recovery.unsaved_work_kept")
    Text { wrapMode: Tui.WordWrap; text: App.recoveryQuestion }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.recovery.restore"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole; onClicked: App.recovery("restore") }
        Button { text: qsTrId("autodoc.dialog.recovery.discard"); DialogButtonBox.buttonRole: DialogButtonBox.DestructiveRole; onClicked: App.recovery("discard") }
        Button { text: qsTrId("autodoc.dialog.recovery.later");   DialogButtonBox.buttonRole: DialogButtonBox.RejectRole; onClicked: App.recovery("later") }
    }
}
