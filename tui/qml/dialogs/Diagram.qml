Dialog {
    closeOnQ: true
    title: App.diagramTitle
    width: 86
    maxWidthPercent: 90
    maxHeightPercent: 85
    helpText: App.diagramHelp
    palette.window: Theme.document.window
    palette.windowText: Theme.document.windowText
    Text { text: App.diagramText; color: Theme.document.text }
    DialogButtonBox {
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
