// TodoFolder.qml — the todos' New…, Load… and M: a project's folder, or the store's new one.
FolderDialog {
    title: "todos: choose the folder"
    currentFolder: App.todoFolder
    dim: false
    onAccepted: App.todoFolderChosen(selectedFolder)
}
