Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "backend version mismatch"
    width: 72
    dim: true
    Text { wrapMode: Tui.WordWrap; text: App.mismatchQuestion }
    DialogButtonBox {
        Button { text: "&Quit"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: "&Restart Now"; visible: App.canRestartMismatch; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.restartMismatch() }
    }
    onRejected: App.quitMismatch()
}
