// AgentProfiles.qml — Options › Agent profiles…: the ways to start an agent, each a name and the
// command that starts it, the default marked ●, the one running ▶. SPC ~ opens the default; Use
// starts another now, asking first when one is running (agent.go, agentprofiles.go).
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: "agent profiles"
    width: 90
    dim: false
    Flex {
        direction: Tui.Vertical
        TableView {
            id: agentTable
            palette.highlight: Theme.document.highlight
            palette.highlightedText: Theme.document.highlightedText
            model: App.agentProfiles
            Layout.fillHeight: true
            onActivated: App.useAgent(index)
            TableViewColumn { role: "default"; title: ""; width: 3 }
            TableViewColumn { role: "name"; title: "NAME"; width: 14 }
            TableViewColumn { role: "command"; title: "COMMAND"; width: 0 }
        }
        Text { text: App.agentProfilesStatus; wrapMode: Tui.WordWrap }
    }
    DialogButtonBox {
        Button { text: "&Use"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.useAgent(agentTable.currentIndex) }
        Button { text: "&Add…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startAddAgent() }
        Button { text: "&Edit…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startEditAgent(agentTable.currentIndex) }
        Button { text: "Make &default"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.makeAgentDefault(agentTable.currentIndex) }
        Button { text: "&Remove"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.removeAgent(agentTable.currentIndex) }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
