// RestartBackend.qml — File › Restart backend…: asks before the daemon stops and the installed
// autodoc starts in its place. No is the default, so Enter cannot restart it by accident.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "restart the backend?"
    width: 64
    dim: false
    standardButtons: Dialog.Yes | Dialog.No
    defaultButton: Dialog.No
    Text { wrapMode: Tui.WordWrap; text: App.restartQuestion }
    onAccepted: App.restartConfirmed()
}
