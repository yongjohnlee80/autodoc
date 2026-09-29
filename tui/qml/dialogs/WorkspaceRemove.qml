// WorkspaceRemove.qml — deleting a workspace asks first, and says what goes (its index) and what
// stays (its files). No answer is a default: Enter alone deletes nothing.
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "delete the workspace?"
    dim: true
    onAccepted: App.removeWorkspaceConfirmed()
    Text { wrapMode: Tui.WordWrap; text: App.removeQuestion }
    DialogButtonBox {
        Button { text: "&No, keep it"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: "&Yes, delete it"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
}
