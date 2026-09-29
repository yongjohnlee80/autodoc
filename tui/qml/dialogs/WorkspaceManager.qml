// WorkspaceManager.qml — the workspaces the store holds: add one, rename or delete the one under
// the cursor. The dialog stays open while each is asked for.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: "workspaces"
    dim: false
    helpText: App.managerHelp
    TableView {
        id: managerTable
        palette.highlight: Theme.document.highlight
        palette.highlightedText: Theme.document.highlightedText
        model: App.managed
        TableViewColumn { role: "name"; title: "NAME"; width: 18 }
        TableViewColumn { role: "state"; title: "STATE"; width: 8 }
        TableViewColumn { role: "root"; title: "ROOT"; width: 0 }
    }
    DialogButtonBox {
        Button { text: "&Add…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startAddWorkspace() }
        Button { text: "&Rename…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startRenameWorkspace(managerTable.currentIndex) }
        Button { text: "&Delete…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startRemoveWorkspace(managerTable.currentIndex) }
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
