// OpenHTML.qml — an HTML file is opened: simplified, its derived text read-only as before, or raw,
// its own HTML to edit (and, in the GUI, its page drawn by Ctrl+T). Simplified takes initial focus:
// Enter opens the file as it always opened.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "open an HTML file"
    Text { wrapMode: Tui.WordWrap; text: App.htmlOpenQuestion }
    DialogButtonBox {
        Button { text: "&Simplified"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole; onClicked: App.openHTMLAs("simplified") }
        Button { text: "&Raw";        DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.openHTMLAs("raw") }
        Button { text: "&Cancel";     DialogButtonBox.buttonRole: DialogButtonBox.RejectRole; onClicked: App.openHTMLAs("cancel") }
    }
}
