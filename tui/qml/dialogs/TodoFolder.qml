// TodoFolder.qml — the todos' New…, Load… and M: a project's folder, or the store's new one.
FolderDialog {
    title: App.todoFolderTitle      // what the folder is for: New…, Load… or M
    currentFolder: App.todoFolder
    dim: false
    onAccepted: App.todoFolderChosen(selectedFolder)
}
