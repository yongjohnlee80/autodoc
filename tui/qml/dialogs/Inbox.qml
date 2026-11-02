// Inbox.qml — SPC a i, :inbox: the user's mail (crewinboxview.go), newest first, fifty at a time.
// The tabs are ways of reading it; the message under the cursor shows below the list, as Markdown.
// Open (Enter) opens it, a report marked read; Reply and Dismiss answer a request; Sender shows the
// agent that sent it; More reads the next page.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 85
    title: App.inboxTitle
    dim: false
    onClosed: App.inboxClosed()
    Flex {
        direction: Tui.Vertical
        Flex {
            direction: Tui.Horizontal
            Button { text: qsTrId("autodoc.dialog.inbox.all"); onClicked: App.inboxTab(0) }
            Button { text: qsTrId("autodoc.dialog.inbox.unread"); onClicked: App.inboxTab(1) }
            Button { text: qsTrId("autodoc.dialog.inbox.requests"); onClicked: App.inboxTab(2) }
            Button { text: qsTrId("autodoc.dialog.inbox.undeliverable"); onClicked: App.inboxTab(3) }
            Text { text: " "; Layout.fillWidth: true }
        }
        TableView {
            id: inboxTable
            palette.highlight: Theme.document.highlight
            palette.highlightedText: Theme.document.highlightedText
            model: App.inbox
            Layout.fillHeight: true
            onCurrentIndexChanged: App.inboxShown(index)
            onActivated: App.inboxOpen(index)
            TableViewColumn { role: "mark"; title: ""; width: 2 }
            TableViewColumn { role: "kind"; title: "KIND"; width: 9 }
            TableViewColumn { role: "from"; title: "FROM"; width: 16 }
            TableViewColumn { role: "subject"; title: "SUBJECT"; width: 0 }
            TableViewColumn { role: "state"; title: "STATE"; width: 10 }
            TableViewColumn { role: "when"; title: "WHEN"; width: 12 }
        }
        Editor {
            readOnly: true
            text: App.inboxDetail
            Layout.fillHeight: true
            SyntaxHighlighter { definition: "Markdown" }
        }
        Text { text: App.inboxStatus; wrapMode: Tui.WordWrap }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.inbox.open"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxOpen(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.mark_read"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxMarkRead(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.reply"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxReply(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.dismiss"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxDismiss(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.sender"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxSender(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.more"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxMore() }
        Button { text: qsTrId("autodoc.dialog.inbox.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
