// Recent.qml — File › Recent files… (SPC r): the files opened last, newest first, with their
// workspace. Enter opens one, entering its workspace when it is another (recent.go).
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: "recent files"
    width: 90
    dim: false
    Flex {
        direction: Tui.Vertical
        TableView {
            id: recentTable
            palette.highlight: Theme.document.highlight
            palette.highlightedText: Theme.document.highlightedText
            model: App.recentFiles
            Layout.fillHeight: true
            onActivated: App.recentSelect(index)
            TableViewColumn { role: "workspace"; title: "WORKSPACE"; width: 16 }
            TableViewColumn { role: "path"; title: "FILE"; width: 0; elideMode: Tui.ElidePath }
        }
        Text { text: App.recentStatus; wrapMode: Tui.WordWrap }
    }
    DialogButtonBox {
        Button { text: "&Open"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.recentSelect(recentTable.currentIndex) }
        Button { text: "C&lear list"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.clearRecent() }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
