// Diagram.qml — File › Preview Mermaid diagram: the block at the cursor drawn by mermaid as an
// image, where the terminal draws one and View › Image previews is on, else its source; the help
// line says which, and why (preview.go). The image scrolls as the HTML preview's does, and Zoom in
// and Zoom out draw it larger or smaller in the same dialog.
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
        Image { id: diagramImage; visible: App.diagramImageShown; scrollable: true; Layout.fillHeight: true }
    }
    DialogButtonBox {
        Button { text: "Zoom &in"; visible: App.diagramImageShown; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.zoomIn() }
        Button { text: "Zoom &out"; visible: App.diagramImageShown; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.zoomOut() }
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
