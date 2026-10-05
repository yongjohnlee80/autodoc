// AgentSwitch.qml — asks before the agent running is stopped for another.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "switch the agent?"
    width: 64
    dim: false
    standardButtons: Dialog.Yes | Dialog.No
    defaultButton: Dialog.No
    Text { wrapMode: Tui.WordWrap; text: App.agentSwitchQuestion }
    onAccepted: App.switchAgentConfirmed()
}
