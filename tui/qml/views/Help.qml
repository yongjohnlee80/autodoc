// Help.qml — the keys, and what each needs.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.view.help.help")
    dim: false
    standardButtons: Dialog.Close
    defaultButton: Dialog.Close
    helpText: qsTrId("autodoc.view.help.q_esc_or_enter_closes_scrolls")
    Editor { readOnly: true; wrap: true; text: App.helpText }
}
