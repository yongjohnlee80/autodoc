// HtmlPreview.qml — File › Preview HTML, as an image of the exported page where the terminal draws
// one and View › Image previews is on (preview.go). An image has no links or selection: Open in
// browser has the page itself.
Dialog {
    closeOnQ: true
    title: App.htmlPreviewTitle
    maxWidthPercent: 95
    maxHeightPercent: 90
    helpText: App.htmlPreviewHelp
    dim: false
    onClosed: App.previewClosed()
    Flex {
        direction: Tui.Vertical
        Image { id: htmlImage; Layout.fillHeight: true }
    }
    DialogButtonBox {
        Button { text: "Open in &browser"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.openPreviewInBrowser() }
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
