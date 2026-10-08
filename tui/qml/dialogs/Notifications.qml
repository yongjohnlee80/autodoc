// Notifications.qml — Go › Notifications… (SPC h): every notification the toasts showed, newest
// first, with the time it came. Clear empties the history.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: App.noticesTitle
    dim: false
    TableView {
        palette.highlight: Theme.document.highlight
        palette.highlightedText: Theme.document.highlightedText
        model: App.notices
        TableViewColumn { role: "when"; title: "TIME"; width: 9 }
        TableViewColumn { role: "text"; title: "NOTIFICATION"; width: 0 }
    }
    DialogButtonBox {
        Button { text: "C&lear"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.clearNotices() }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onRejected: App.noticesClosed()
}
