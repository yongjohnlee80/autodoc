// CrewBridges.qml — System › Crew Bridges…: the crew's helper processes on this machine, each with
// its owner and whether it is orphaned (crewbridges.go), listed again every few seconds while open.
// Kill ends the row under the cursor; Kill orphans… every orphan; Copy puts a row's details on the
// clipboard.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.crew_bridges.title")
    dim: false
    onClosed: App.crewBridgesClosed()
    Flex {
        direction: Tui.Vertical
        TableView {
            id: bridgeTable
            palette.highlight: Theme.document.highlight
            palette.highlightedText: Theme.document.highlightedText
            model: App.crewBridges
            Layout.fillHeight: true
            onCurrentIndexChanged: App.crewBridgeShown(index)
            TableViewColumn { role: "kind"; title: "KIND"; width: 20 }
            TableViewColumn { role: "owner"; title: "OWNER"; width: 0 }
            TableViewColumn { role: "host"; title: "HOST"; width: 16 }
            TableViewColumn { role: "pid"; title: "PID"; width: 8 }
            TableViewColumn { role: "started"; title: "STARTED"; width: 13 }
            TableViewColumn { role: "state"; title: "STATE"; width: 24 }
        }
        Text { text: App.crewBridgesDetail; wrapMode: Tui.WordWrap }
        Text { text: App.crewBridgesStatus; wrapMode: Tui.WordWrap }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.crew_bridges.kill"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.crewBridgeKill(bridgeTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.crew_bridges.kill_orphans"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.crewBridgesKillOrphans() }
        Button { text: qsTrId("autodoc.dialog.crew_bridges.copy"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.crewBridgeCopy(bridgeTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.crew_bridges.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
