// RankerWindow.qml — how many of the top candidates the ranker in use ranks. One outside the bounds
// opens it again with the reason on its help line.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.rankerWindow.the_ranker_s_window")
    width: 64
    dim: false
    standardButtons: Dialog.Save | Dialog.Cancel
    defaultButton: Dialog.Save
    helpText: App.rankerWindowError
    Flex {
        direction: Tui.Vertical
        Text { text: qsTrId("autodoc.dialog.rankerWindow.candidates_ranked") }
        TextField { id: rWindow; text: App.rankerWindow }
    }
    onAccepted: App.saveRankerWindow(rWindow.text)
}
