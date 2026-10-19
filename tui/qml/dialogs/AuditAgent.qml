// AuditAgent.qml — an install card's Audit…: the picker of AutoDoc's own agents to hand the audit
// to, the default first; on the right what the agent runs. Choosing one hands it a fresh copy of the
// candidate and the task file; closing it brings the card back (candidate.go).
Picker {
    name: "auditAgent"
    title: "audit with an agent"
    label: "filter: a part of an agent's name"
    fieldText: App.auditAgentField
    model: App.auditAgentRows
    listTitle: App.auditAgentStatus
    previewTitle: App.auditAgentPreviewTitle
    previewText: App.auditAgentPreviewText
    previewAt: App.auditAgentPreviewAt
    acceptText: "&Audit"
    TableViewColumn { role: "agent"; title: "AGENT"; width: 0 }
    TableViewColumn { role: "state"; title: "STATE"; width: 9 }
}
