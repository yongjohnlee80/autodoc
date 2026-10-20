// PluginConfirm.qml — before a plugin is added or updated: what it is, where it came from, what
// its build runs and what it starts, and that it is the user's risk. No answer is a default: Enter
// alone runs nothing, and a refusal throws the clone away. Audit… hands a copy to an agent first
// (candidate.go).
Dialog {
    closeOnQ: true
    maxWidthPercent: 85
    maxHeightPercent: 85
    title: App.pluginConfirmTitle
    dim: true
    onAccepted: App.pluginConfirmed()
    onRejected: App.pluginDeclined()
    Text { wrapMode: Tui.WordWrap; text: App.pluginQuestion }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.pluginConfirm.no_cancel"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: qsTrId("autodoc.dialog.pluginConfirm.audit"); DialogButtonBox.buttonRole: DialogButtonBox.DestructiveRole; onClicked: App.pluginAudit() }
        Button { text: qsTrId("autodoc.dialog.pluginConfirm.yes_at_my_own_risk"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
}
