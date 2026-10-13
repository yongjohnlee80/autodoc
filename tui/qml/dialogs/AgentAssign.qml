// AgentAssign.qml — the todos panel's A: the picker of AutoDoc's own agents to hand the task to, by
// name and codename, running or stopped; on the right what the agent runs. Choosing one asks for the
// Instructions: line (TodoAssign.qml), then assigns the task (todos_assign.go).
Picker {
    name: "agentAssign"
    title: "assign to an agent"
    label: "filter: a part of an agent's name"
    fieldText: App.agentAssignField
    model: App.agentAssignRows
    listTitle: App.agentAssignStatus
    previewTitle: App.agentAssignPreviewTitle
    previewText: App.agentAssignPreviewText
    previewAt: App.agentAssignPreviewAt
    acceptText: "&Choose"
    TableViewColumn { role: "agent"; title: "AGENT"; width: 0 }
    TableViewColumn { role: "state"; title: "STATE"; width: 9 }
}
