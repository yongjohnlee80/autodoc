// Diagram.qml — File › Preview Mermaid diagram: the block at the cursor drawn by mermaid as an
// image, where the terminal draws one and View › Image previews is on, else its source; the help
// line says which, and why (preview.go). In a GUI window it is drawn natively, by golib's Mermaid,
// in the HTML view, where File › Preview option chooses the native engine or no browser is found.
// The image scrolls as the HTML preview's does, and Zoom in and Zoom out draw it larger or smaller
// in the same dialog; a native drawing has them disabled.
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
        HTMLView { visible: App.diagramNativeShown; Layout.fillHeight: true }
    }
    DialogButtonBox {
        // a native drawing has no zoom: its buttons show, disabled
        Button { text: "Zoom &in"; visible: App.diagramDrawn; enabled: App.diagramImageShown; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.zoomIn() }
        Button { text: "Zoom &out"; visible: App.diagramDrawn; enabled: App.diagramImageShown; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.zoomOut() }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
