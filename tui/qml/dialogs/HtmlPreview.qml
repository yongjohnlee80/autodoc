// HtmlPreview.qml — File › Preview HTML, as an image of the whole exported page where the terminal
// draws one and View › Image previews is on (preview.go). The image scrolls (the arrows, j k h l,
// Page Up/Down, [ ], Home/End, the wheel); Zoom in and Zoom out render it again larger or smaller,
// the dialog as it is. An image has no links or selection: Open in browser has the page itself.
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
        Image { id: htmlImage; scrollable: true; Layout.fillHeight: true }
    }
    DialogButtonBox {
        Button { text: "Zoom &in"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.zoomIn() }
        Button { text: "Zoom &out"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.zoomOut() }
        Button { text: "Open in &browser"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.openPreviewInBrowser() }
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
