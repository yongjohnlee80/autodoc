// InboxMessage.qml — the Inbox's Open (Enter): one message read whole (crewinboxview.go: openItem), as
// Markdown, Rendered in the GUI's window (a terminal's Editor stays Raw), read-only. Its Editor takes
// the letters, so the one key besides Esc is q, which closes it, back to the Inbox's list.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 85
    width: 84
    title: App.inboxMessageTitle
    dim: false
    onClosed: App.inboxMessageClosed()
    Flex {
        direction: Tui.Vertical
        Editor {
            cursorColor: Theme.document.cursor
            Layout.fillHeight: true
            readOnly: true
            wrap: true
            view: Editor.Rendered
            text: App.inboxMessageText
            SyntaxHighlighter { definition: "Markdown" }
        }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.inbox.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
