// Reply.qml — the reply to an agent's decision request (crewreply.go): its question as the title,
// its body above, the answer typed below (Enter breaks the line; Tab reaches the buttons). Send
// answers it and Dismiss declines it, its sender told either way; Cancel leaves it open. An answer
// that could not be sent opens here again (App.replyDraft).
Dialog {
    maxWidthPercent: 90
    maxHeightPercent: 85
    title: App.replyTitle
    width: 84
    dim: false
    helpText: qsTrId("autodoc.dialog.reply.tab_reaches_the_buttons")
    Flex {
        direction: Tui.Vertical
        Text { wrapMode: Tui.WordWrap; text: App.replyText }
        TextArea { id: reply; focus: true; text: App.replyDraft; placeholderText: qsTrId("autodoc.dialog.reply.your_answer"); wrapMode: TextEdit.WordWrap }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.reply.cancel"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: qsTrId("autodoc.dialog.reply.dismiss"); DialogButtonBox.buttonRole: DialogButtonBox.DestructiveRole; onClicked: App.replyDismiss() }
        Button { text: qsTrId("autodoc.dialog.reply.send"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
    onAccepted: App.replySend(reply.text)
    onClosed: App.replyClosed()
}
