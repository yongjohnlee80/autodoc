// WorkspaceSchema.qml — the workspace's frontmatter schema: the YAML file that declares its notes'
// fields (ADR 0212 §5). The path is stored as typed: under the root, or absolute; blank removes it.
Dialog {
    closeOnQ: false
    title: App.schemaTitle
    width: 76
    maxWidthPercent: 90
    helpText: App.schemaHelp
    Flex {
        direction: Tui.Vertical
        Text { text: App.schemaRoot }
        Text { text: "schema file (relative to the root, or absolute; blank for none)" }
        TextField { id: schemaPath; text: App.schemaPath }
        Text { text: App.schemaState }
    }
    DialogButtonBox {
        Button { text: "&Save"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "&Cancel"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.saveSchema(schemaPath.text)
}
