// WorkspaceAdd.qml — a new workspace: its title, then its folder, chosen in the picker's layout.
// The folder field follows the listing, and a path typed there lists it; its **/*.md are indexed,
// .git skipped. Select adds it; a refusal opens it again, the reason on the status line.
FolderDialog {
    title: "add a workspace"
    currentFolder: App.home
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: "title" }
        TextField { id: wsName; text: App.wsTitle }
    }
    onAccepted: App.addWorkspace(wsName.text, selectedFolder)
}
