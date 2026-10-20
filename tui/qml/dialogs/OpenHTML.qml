// OpenHTML.qml — an HTML file is opened: simplified, its derived text read-only as before, or raw,
// its own HTML to edit (and, in the GUI, its page drawn by Ctrl+T). Simplified takes initial focus:
// Enter opens the file as it always opened.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.openHTML.open_an_html_file")
    Text { wrapMode: Tui.WordWrap; text: App.htmlOpenQuestion }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.openHTML.simplified"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole; onClicked: App.openHTMLAs("simplified") }
        Button { text: qsTrId("autodoc.dialog.openHTML.raw");        DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.openHTMLAs("raw") }
        Button { text: qsTrId("autodoc.dialog.openHTML.cancel");     DialogButtonBox.buttonRole: DialogButtonBox.RejectRole; onClicked: App.openHTMLAs("cancel") }
    }
}
