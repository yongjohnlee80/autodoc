// Registrations.qml — the backend lacks file types this build reads (ADR 0216): asked once a
// session. Restart Now stops it and starts this build in its place; Not Now keeps it, and System ›
// Restart backend… can still restart it later.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "this build reads more"
    width: 72
    dim: false
    Text { wrapMode: Tui.WordWrap; text: App.registrationQuestion }
    DialogButtonBox {
        Button { text: "&Not Now"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: "&Restart Now"; visible: App.canRestartRegistrations; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.restartForRegistrations() }
    }
}
