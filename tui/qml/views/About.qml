// About.qml — the version, the author and the license, and where the daemon and its log are.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "about autodoc"
    dim: false
    standardButtons: Dialog.Close
    defaultButton: Dialog.Close
    Text { wrapMode: Tui.WordWrap; text: App.aboutText }
}
