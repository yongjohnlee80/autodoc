// TodoFolder.qml — the todos' New…, Load… and M: a project's folder, or the store's new one. Each
// has a use of its own in main.qml, whose title says what the folder is for.
FolderDialog {
    title: "todos: choose the folder"
    currentFolder: App.todoFolder
    dim: false
    onAccepted: App.todoFolderChosen(selectedFolder)
}
