// WorkspaceManager.qml — the workspaces the store holds, by title and directory on the left, and
// the settings of the one under the cursor on the right, as the settings dialog groups them: Edit,
// then Advanced. Add…, Edit… and Advanced… open that dialog; Delete… asks first. The manager stays
// open while each is asked for.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 85
    title: qsTrId("autodoc.dialog.workspaceManager.workspaces")
    dim: false
    helpText: App.managerHelp
    onClosed: App.managerClosed()
    Split {
        orientation: Tui.Horizontal
        ratio: 0.4
        Frame {
            title: qsTrId("autodoc.dialog.workspaceManager.workspaces")
            TableView {
                id: managerTable
                palette.highlight: Theme.document.highlight
                palette.highlightedText: Theme.document.highlightedText
                model: App.managed
                currentIndex: App.managerIndex
                onCurrentIndexChanged: App.managerMoved(index)
                TableViewColumn { role: "name"; title: "TITLE"; width: 16 }
                TableViewColumn { role: "root"; title: "DIRECTORY"; width: 0; elideMode: Tui.ElidePath }
            }
        }
        Frame {
            title: qsTrId("autodoc.dialog.workspaceManager.settings")
            Text { text: App.managerDetail; wrapMode: Tui.WordWrap }
        }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.workspaceManager.add"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startAddWorkspace() }
        Button { text: qsTrId("autodoc.dialog.workspaceManager.edit"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startEditWorkspace(managerTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.workspaceManager.advanced"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startAdvancedWorkspace(managerTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.workspaceManager.delete"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startRemoveWorkspace(managerTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.workspaceManager.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
