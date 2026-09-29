// WorkspaceRename.qml — a new name for the workspace under the manager's cursor. Its index is
// kept; only the name changes. A refusal opens it again with the reason on its help line.
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "rename the workspace"
    width: 64
    standardButtons: Dialog.Ok | Dialog.Cancel
    defaultButton: Dialog.Ok
    helpText: App.workspaceRenameError
    TextField { id: wsTo; text: App.renameFrom }
    onAccepted: App.renameWorkspace(wsTo.text)
}
