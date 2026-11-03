// Grants.qml — Go › Agent permissions…, :permissions: every capability granted, one row each, by
// holder (grants.go). Revoke takes the row's capability back, Revoke all everything its holder holds,
// each asked first; the holder's next use of it asks again by its card. The list follows the
// preference while the dialog is open.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.grants.title")
    dim: false
    onClosed: App.grantsClosed()
    Flex {
        direction: Tui.Vertical
        TableView {
            id: grantTable
            palette.highlight: Theme.document.highlight
            palette.highlightedText: Theme.document.highlightedText
            model: App.grants
            currentIndex: App.grantsIndex
            Layout.fillHeight: true
            onCurrentIndexChanged: App.grantShown(index)
            TableViewColumn { role: "who"; title: "WHO"; width: 20 }
            TableViewColumn { role: "kind"; title: "KIND"; width: 10 }
            TableViewColumn { role: "capability"; title: "CAPABILITY"; width: 16 }
            TableViewColumn { role: "what"; title: "LETS IT"; width: 0 }
        }
        Text { text: App.grantsDetail; wrapMode: Tui.WordWrap }
        Text { text: App.grantsStatus; wrapMode: Tui.WordWrap }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.grants.revoke"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.grantRevoke(grantTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.grants.revoke_all"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.grantRevokeAll(grantTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.grants.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
