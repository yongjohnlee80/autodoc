// PurgeModel.qml — asks before a model no longer used loses its vectors.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "purge the model?"
    width: 64
    dim: false
    standardButtons: Dialog.Yes | Dialog.No
    defaultButton: Dialog.No
    Text { wrapMode: Tui.WordWrap; text: App.purgeQuestion }
    onAccepted: App.purgeConfirmed()
}
