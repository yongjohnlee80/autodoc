// WorkspaceRoot.qml — the folder a workspace being added indexes, chosen in the picker's layout
// over the settings dialog (Browse…). The folder field follows the listing, and a path typed there
// lists it; Select puts the folder in the dialog's root.
FolderDialog {
    title: qsTrId("autodoc.dialog.workspaceRoot.the_workspace_s_root")
    currentFolder: App.browseFolder
    dim: false
    onAccepted: App.rootChosen(selectedFolder)
}
