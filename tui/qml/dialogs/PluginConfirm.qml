// PluginConfirm.qml — before a plugin is added or updated: what it is, where it came from, what
// its build runs and what it starts, and that it is the user's risk. No answer is a default: Enter
// alone runs nothing, and a refusal throws the clone away.
Dialog {
    closeOnQ: false
    maxWidthPercent: 85
    maxHeightPercent: 85
    title: App.pluginConfirmTitle
    dim: true
    onAccepted: App.pluginConfirmed()
    onRejected: App.pluginDeclined()
    Text { wrapMode: Tui.WordWrap; text: App.pluginQuestion }
    DialogButtonBox {
        Button { text: "&No, cancel"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: "&Yes, at my own risk"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
}
