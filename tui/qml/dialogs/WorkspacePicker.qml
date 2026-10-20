// WorkspacePicker.qml — Go › Workspace: the daemon's workspaces. Manage… adds, renames and deletes
// them (WorkspaceManager).
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.workspacePicker.workspace")
    dim: false
    helpText: qsTrId("autodoc.dialog.workspacePicker.enter_switches_manage_adds_renames_or_deletes_on")
    ListView {
        palette.highlight: Theme.document.highlight
        palette.highlightedText: Theme.document.highlightedText
        model: App.workspaces
        currentIndex: App.workspaceIndex
        textRole: "label"
        onActivated: App.useWorkspace(index)
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.workspacePicker.manage"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.manageWorkspaces() }
        Button { text: qsTrId("autodoc.dialog.workspacePicker.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
