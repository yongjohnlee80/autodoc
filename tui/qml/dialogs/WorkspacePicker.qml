// WorkspacePicker.qml — Go › Workspace: the daemon's workspaces. Manage… adds, renames and deletes
// them (WorkspaceManager).
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "workspace"
    dim: false
    helpText: "Enter switches · Manage… adds, renames or deletes one"
    ListView {
        palette.highlight: Theme.document.highlight
        palette.highlightedText: Theme.document.highlightedText
        model: App.workspaces
        textRole: "label"
        onActivated: App.useWorkspace(index)
    }
    DialogButtonBox {
        Button { text: "&Manage…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.manageWorkspaces() }
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
