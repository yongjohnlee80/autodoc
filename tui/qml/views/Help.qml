// Help.qml — the keys, and what each needs.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "help"
    dim: false
    standardButtons: Dialog.Close
    defaultButton: Dialog.Close
    helpText: "q, Esc or Enter closes · scrolls"
    Editor { readOnly: true; wrap: true; text: App.helpText }
}
