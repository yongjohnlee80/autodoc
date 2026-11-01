// AgentEdit.qml — an agent profile: display name, stable codename, the command that starts the
// agent, run by the shell as written ({root} and {workspace} in it are filled in), its kind (the
// command's, or a CLI's by name: a wrapper of Claude Code is still Claude Code), and the env set on
// its process (NAME=value words; ~ and $VAR expanded at its start, never by a shell). A refusal
// opens it again with the reason on its help line.
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
        Text { text: qsTrId("autodoc.dialog.agentEdit.kind") }
        ComboBox { model: App.agentKinds; textRole: "label"; currentIndex: App.agentKindIndex; onActivated: App.agentKindPicked(index) }
        Text { text: qsTrId("autodoc.dialog.agentEdit.env") }
        TextField { id: aEnv; text: App.agentEnv; placeholderText: qsTrId("autodoc.dialog.agentEdit.env_placeholder") }
        Text { wrapMode: Tui.WordWrap; text: qsTrId("autodoc.dialog.agentEdit.env_not_for_secrets") }
        Text { wrapMode: Tui.WordWrap; text: qsTrId("autodoc.dialog.agentEdit.the_agent_starts_in_autodoc_s_own_folder_for_the") }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.agentEdit.save"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: qsTrId("autodoc.dialog.agentEdit.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.saveAgentProfileWithEnv(aName.text, aCommand.text, aCodename.text, aEnv.text)
}
