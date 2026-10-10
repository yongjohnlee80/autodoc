// PluginRemove.qml — removing a plugin asks first, and says what goes: its directory, with what its
// build made. No answer is a default: Enter alone removes nothing.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "remove the plugin?"
    dim: true
    onAccepted: App.removePluginConfirmed()
    Text { wrapMode: Tui.WordWrap; text: App.removePluginQuestion }
    DialogButtonBox {
        Button { text: "&No, keep it"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: "&Yes, remove it"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
}
