// RankerRemove.qml — asks before a ranker goes, with its key, usage and log.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "remove the ranker?"
    width: 64
    dim: false
    standardButtons: Dialog.Yes | Dialog.No
    defaultButton: Dialog.No
    Text { wrapMode: Tui.WordWrap; text: App.rankerRemoveQuestion }
    onAccepted: App.removeRankerConfirmed()
}
