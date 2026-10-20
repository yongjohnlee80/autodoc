Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.mismatch.backend_version_mismatch")
    width: 72
    dim: true
    Text { wrapMode: Tui.WordWrap; text: App.mismatchQuestion }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.mismatch.quit"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: qsTrId("autodoc.dialog.mismatch.restart_now"); visible: App.canRestartMismatch; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.restartMismatch() }
    }
    onRejected: App.quitMismatch()
}
