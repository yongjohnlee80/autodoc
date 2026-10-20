// AgentSetup.qml — the agent setup (agentsetup.go): on a first run, or from System › Set up an
// agent…. With an agent CLI found on PATH: why an agent helps, the CLIs found (the first chosen),
// the profile's name, and Set up or No. With none: which to install and why, and Close.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.agentSetup.set_up_an_ai_agent")
    width: 68
    dim: true
    Flex {
        direction: Tui.Vertical
        Text { wrapMode: Tui.WordWrap; visible: App.agentSetupSome; text: qsTrId("autodoc.dialog.agentSetup.an_ai_agent_can_set_autodoc_up_for_you_and_help") }
        Text { visible: App.agentSetupSome; text: qsTrId("autodoc.dialog.agentSetup.agent_cli") }
        ComboBox { id: sCLI; visible: App.agentSetupSome; model: App.agentSetupCLIs; textRole: "label"; currentIndex: App.agentSetupIndex; onActivated: App.agentSetupPicked(index, sName.text) }
        Text { visible: App.agentSetupSome; text: qsTrId("autodoc.dialog.agentSetup.name") }
        TextField { id: sName; visible: App.agentSetupSome; text: App.agentSetupName }
        Text { wrapMode: Tui.WordWrap; visible: App.agentSetupSome; text: qsTrId("autodoc.dialog.agentSetup.found_on_your_path_another_one_system_agent_prof") }
        Text { wrapMode: Tui.WordWrap; visible: App.agentSetupNone; text: qsTrId("autodoc.dialog.agentSetup.no_agent_cli_was_found_on_your_path_install_one") }
    }
    DialogButtonBox {
        Button { text: App.agentSetupReject; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: qsTrId("autodoc.dialog.agentSetup.set_up"); visible: App.agentSetupSome; enabled: App.agentSetupSome; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
    onAccepted: App.agentSetupConfirm(sCLI.currentIndex, sName.text)
    onRejected: App.agentSetupDecline()
}
