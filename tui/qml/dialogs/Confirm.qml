// Confirm.qml — the one yes-or-no question a feature asks (Core.Ask, ask.go) before it does what it
// cannot undo. No comes first, so Enter alone does nothing; y and n answer by their mnemonics.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: App.askTitle
    width: 64
    dim: false
    onAccepted: App.askAccepted()
    onRejected: App.askRejected()
    onClosed: App.askClosed()
    Text { wrapMode: Tui.WordWrap; text: App.askText }
    DialogButtonBox {
        Button { text: App.askNo; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: App.askYes; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
}
