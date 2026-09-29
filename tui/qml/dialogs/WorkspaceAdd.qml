// WorkspaceAdd.qml — a new workspace: its name, and its root, a directory (~/ is the home). It
// indexes **/*.md there, skipping .git. A refusal opens it again with the reason on its help line
// (App.workspaceAddError).
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "add a workspace"
    width: 64
    standardButtons: Dialog.Ok | Dialog.Cancel
    defaultButton: Dialog.Ok
    helpText: App.workspaceAddError
    Flex {
        direction: Tui.Vertical
        TextField { id: wsName; placeholderText: "name, e.g. kb" }
        TextField { id: wsRoot; placeholderText: "root directory, e.g. ~/notes" }
    }
    onOpened: { wsName.clear(); wsRoot.clear() }
    onAccepted: App.addWorkspace(wsName.text, wsRoot.text)
}
