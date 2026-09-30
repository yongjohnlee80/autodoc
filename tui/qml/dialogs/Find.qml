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
    Flex {
        direction: Tui.Vertical
        Text { text: "a word or phrase, case-blind" }
        TextField { id: pattern; text: App.lastFind }
    }
    onAccepted: App.find(pattern.text)
    onRejected: App.findCancelled()
}
