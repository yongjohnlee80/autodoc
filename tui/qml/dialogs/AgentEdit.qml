// AgentEdit.qml — an agent profile: display name, stable codename and the command that starts the
// agent, run by the shell as written; {root} and {workspace} in it are filled in. A refusal opens
// it again with the reason on its help line.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: App.agentFormTitle
    width: 84
    dim: false
    helpText: App.agentFormError
    Flex {
        direction: Tui.Vertical
        Text { text: qsTrId("autodoc.dialog.agentEdit.name") }
        TextField { id: aName; text: App.agentName; placeholderText: qsTrId("autodoc.dialog.agentEdit.claude_codex") }
        Text { text: qsTrId("autodoc.dialog.agentEdit.codename_leave_empty_to_derive_from_the_name") }
        TextField { id: aCodename; text: App.agentCodename; placeholderText: qsTrId("autodoc.dialog.agentEdit.claude_opus") }
        Text { text: qsTrId("autodoc.dialog.agentEdit.command") }
        TextField { id: aCommand; text: App.agentCommand; placeholderText: qsTrId("autodoc.dialog.agentEdit.claude_model_opus_codex_opencode_root") }
        Text { wrapMode: Tui.WordWrap; text: qsTrId("autodoc.dialog.agentEdit.the_agent_starts_in_autodoc_s_own_folder_for_the") }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.agentEdit.save"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: qsTrId("autodoc.dialog.agentEdit.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.saveAgentProfile(aName.text, aCommand.text, aCodename.text)
}
