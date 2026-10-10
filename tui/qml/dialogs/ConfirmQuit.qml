// ConfirmQuit.qml — quitting over unsaved changes asks first. The answers name both outcomes.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "quit autodoc?"
    dim: true
    onAccepted: App.quitConfirmed()
    Text { wrapMode: Tui.WordWrap; text: App.quitQuestion }
    DialogButtonBox {
        Button { text: "&Yes, quit"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "&No, stay";  DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
