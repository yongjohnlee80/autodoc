// WorkspacePicker.qml — Go › Workspace: the daemon's workspaces; a busy one is served elsewhere.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "workspace"
    dim: false
    standardButtons: Dialog.Close
    helpText: "Enter switches · a busy workspace is served by another autodoc"
    ListView {
        palette.highlight: Theme.document.highlight
        palette.highlightedText: Theme.document.highlightedText
        model: App.workspaces
        textRole: "label"
        onActivated: App.useWorkspace(index)
    }
}
