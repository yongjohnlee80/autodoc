// AgentEdit.qml — an agent profile, added or edited: its name and the command that starts the
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
        Text { text: "name" }
        TextField { id: aName; text: App.agentName; placeholderText: "claude, codex…" }
        Text { text: "command" }
        TextField { id: aCommand; text: App.agentCommand; placeholderText: "claude --model opus, codex, opencode {root}…" }
        Text { wrapMode: Tui.WordWrap; text: "The agent starts in AutoDoc's own folder for the workspace, told about it in AGENTS.md and CLAUDE.md; $AUTODOC_ROOT and $AUTODOC_WORKSPACE name the workspace." }
    }
    DialogButtonBox {
        Button { text: "&Save"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.saveAgent(aName.text, aCommand.text)
}
