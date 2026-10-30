// Registrations.qml — the backend lacks file types this build reads
// (docs/decisions/search.md#registered-chunkers-and-derivers): asked once a session. Restart Now
// stops it and starts this build in its place; Not Now keeps it, and System › Restart backend… can
// still restart it later.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.registrations.this_build_reads_more")
    width: 72
    dim: false
    Text { wrapMode: Tui.WordWrap; text: App.registrationQuestion }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.registrations.not_now"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: qsTrId("autodoc.dialog.registrations.restart_now"); visible: App.canRestartRegistrations; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.restartForRegistrations() }
    }
}
