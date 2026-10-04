// Diagram.qml — File › Preview Mermaid diagram: the block at the cursor as an image, where the
// terminal draws one and View › Image previews is on, else as a terminal graph; the help line says
// which, and why (preview.go).
Dialog {
    closeOnQ: true
    title: App.diagramTitle
    width: 86
    maxWidthPercent: 90
    maxHeightPercent: 85
    helpText: App.diagramHelp
    palette.window: Theme.document.window
    palette.windowText: Theme.document.windowText
    onClosed: App.previewClosed()
    Flex {
        direction: Tui.Vertical
        Text { visible: App.diagramTextShown; text: App.diagramText; color: Theme.document.text }
        Image { id: diagramImage; visible: App.diagramImageShown; Layout.fillHeight: true }
    }
    DialogButtonBox {
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
