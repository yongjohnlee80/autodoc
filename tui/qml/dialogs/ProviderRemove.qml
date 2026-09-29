// ProviderRemove.qml — asks before an embedding provider goes, with its key, usage and log.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "remove the provider?"
    width: 64
    dim: false
    standardButtons: Dialog.Yes | Dialog.No
    defaultButton: Dialog.No
    Text { wrapMode: Tui.WordWrap; text: App.providerRemoveQuestion }
    onAccepted: App.removeProviderConfirmed()
}
