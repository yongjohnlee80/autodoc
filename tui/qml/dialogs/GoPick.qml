// GoPick.qml — SPC Tab, Go › Widget… and :go alone: the widgets the keyboard can go to (widgets.go),
// the editor, the panels and the terminal's and agent's tabs; never the menu bar. The focused one is
// under the cursor. Enter goes there, opening a closed panel; Tab and Shift+Tab move the cursor,
// wrapping, as Down and Up do. q closes it.
Dialog {
    closeOnQ: true
    maxWidthPercent: 60
    maxHeightPercent: 80
    title: "go to"
    dim: false
    helpText: "Enter goes · Tab and Shift+Tab move · :go <name> from the command line"
    Shortcut { sequence: "Tab"; onActivated: App.widgetNext() }
    Shortcut { sequence: "Shift+Tab"; onActivated: App.widgetPrevious() }
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
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
