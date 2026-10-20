// GoPick.qml — SPC Tab, Go › Widget… and :go alone: the widgets the keyboard can go to (widgets.go),
// the editor, the panels and the terminal's and agent's tabs; never the menu bar. The focused one is
// under the cursor. Enter goes there, opening a closed panel; Tab and Shift+Tab move the cursor,
// wrapping, as Down and Up do. q closes it.
Dialog {
    closeOnQ: true
    keyScope: "autodoc.goPick" // its keys' commands (tui/keys.go: dialogCommands)
    maxWidthPercent: 60
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.goPick.go_to")
    dim: false
    helpText: qsTrId("autodoc.dialog.goPick.enter_goes_tab_and_shift_tab_move_go_name_from_t")
    Shortcut { sequence: keyId("autodoc.goPick.next"); onActivated: App.widgetNext() }
    Shortcut { sequence: keyId("autodoc.goPick.previous"); onActivated: App.widgetPrevious() }
    TableView {
        palette.highlight: Theme.document.highlight
        palette.highlightedText: Theme.document.highlightedText
        model: App.widgetRows
        currentIndex: App.widgetIndex
        onCurrentIndexChanged: App.widgetMoved(index)
        onActivated: App.goWidget(index)
        TableViewColumn { role: "label"; title: "WIDGET"; width: 0 }
        TableViewColumn { role: "state"; title: ""; width: 7 }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.goPick.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
