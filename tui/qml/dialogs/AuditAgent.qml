// AuditAgent.qml — an install card's Audit…: the picker of AutoDoc's own agents to hand the audit
// to, the default first; on the right what the agent runs. Choosing one hands it a fresh copy of the
// candidate and the task file; closing it brings the card back (candidate.go).
Picker {
    name: "auditAgent"
    title: qsTrId("autodoc.dialog.auditAgent.audit_with_an_agent")
    label: qsTrId("autodoc.dialog.auditAgent.filter_a_part_of_an_agent_s_name")
    fieldText: App.auditAgentField
    model: App.auditAgentRows
    listTitle: App.auditAgentStatus
    previewTitle: App.auditAgentPreviewTitle
    previewText: App.auditAgentPreviewText
    previewAt: App.auditAgentPreviewAt
    acceptText: qsTrId("autodoc.dialog.auditAgent.audit")
    TableViewColumn { role: "agent"; title: "AGENT"; width: 0 }
    TableViewColumn { role: "state"; title: "STATE"; width: 9 }
}
