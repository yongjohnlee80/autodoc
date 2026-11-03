// Inbox.qml — SPC a i, :inbox, Go › Inbox: the user's mail (crewinboxview.go), newest first, fifty at
// a time. The tabs are ways of reading it; the message under the cursor shows below the list, as
// Markdown. Open (Enter) reads it whole in a dialog of its own (InboxMessage.qml), a report marked
// read; Reply and Dismiss answer a request; Sender shows the agent that sent it; More reads the next
// page while there is one. The list has the keyboard: every tab's and button's letter reaches it from
// there, and each says what it does when it has focus (its tooltip). The cursor stays on its message
// when the list is read again.
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
            Button { text: qsTrId("autodoc.dialog.inbox.all"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_all"); onClicked: App.inboxTab(0) }
            Button { text: qsTrId("autodoc.dialog.inbox.unread"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_unread"); onClicked: App.inboxTab(1) }
            Button { text: qsTrId("autodoc.dialog.inbox.requests"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_requests"); onClicked: App.inboxTab(2) }
            Button { text: qsTrId("autodoc.dialog.inbox.undeliverable"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_undeliverable"); onClicked: App.inboxTab(3) }
            Text { text: " "; Layout.fillWidth: true }
        }
        TableView {
            id: inboxTable
            focus: true
            palette.highlight: Theme.document.highlight
            palette.highlightedText: Theme.document.highlightedText
            model: App.inbox
            currentIndex: App.inboxIndex
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
        Button { text: qsTrId("autodoc.dialog.inbox.open"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_open"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxOpen(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.mark_read"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_mark_read"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxMarkRead(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.reply"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_reply"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxReply(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.dismiss"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_dismiss"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxDismiss(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.sender"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_sender"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxSender(inboxTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.inbox.more"); toolTipText: qsTrId("autodoc.dialog.inbox.tip_more"); enabled: App.inboxCanMore; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.inboxMore() }
        Button { text: qsTrId("autodoc.dialog.inbox.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
