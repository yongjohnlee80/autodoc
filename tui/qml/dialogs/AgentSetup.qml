// AgentSetup.qml — the agent setup (agentsetup.go): on a first run, or from System › Set up an
// agent…. With an agent CLI found on PATH: why an agent helps, the CLIs found (the first chosen),
// the profile's name, and Set up or No. With none: which to install and why, and Close.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: "Set up an AI agent?"
    width: 68
    dim: true
    Flex {
        direction: Tui.Vertical
        Text { wrapMode: Tui.WordWrap; visible: App.agentSetupSome; text: "An AI agent can set AutoDoc up for you and help you write: it saves time getting started." }
        Text { visible: App.agentSetupSome; text: "Agent CLI" }
        ComboBox { id: sCLI; visible: App.agentSetupSome; model: App.agentSetupCLIs; textRole: "label"; currentIndex: App.agentSetupIndex; onActivated: App.agentSetupPicked(index, sName.text) }
        Text { visible: App.agentSetupSome; text: "Name" }
        TextField { id: sName; visible: App.agentSetupSome; text: App.agentSetupName }
        Text { wrapMode: Tui.WordWrap; visible: App.agentSetupSome; text: "Found on your PATH. Another one? System › Agent profiles…" }
        Text { wrapMode: Tui.WordWrap; visible: App.agentSetupNone; text: "No agent CLI was found on your PATH. Install one of Claude Code, Codex, Grok or Antigravity: an agent can set AutoDoc up for you and help you write. System › Set up an agent… brings this back." }
    }
    DialogButtonBox {
        Button { text: App.agentSetupReject; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: "&Set up"; visible: App.agentSetupSome; enabled: App.agentSetupSome; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
    onAccepted: App.agentSetupConfirm(sCLI.currentIndex, sName.text)
    onRejected: App.agentSetupDecline()
}
