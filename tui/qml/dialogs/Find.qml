// Find.qml — / : a word or phrase to find in the pane that had the keyboard (the page, the explorer
// or the links); n and N then find it again. The workspace's search is Ctrl+G, SPC / or SPC SPC.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: App.findTitle
    dim: false
    width: 48
    standardButtons: Dialog.Ok | Dialog.Cancel
    defaultButton: Dialog.Ok
    helpText: App.findError
    TextField { id: pattern; text: App.lastFind; placeholderText: "a word or phrase" }
    onAccepted: App.find(pattern.text)
    onRejected: App.findCancelled()
}
