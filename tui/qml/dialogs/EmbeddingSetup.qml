// EmbeddingSetup.qml — Search models › Embedding Models › Set up… (embedsetup.go,
// docs/decisions/search.md#the-setup-dialog): the Ollama to ask and what this machine has, the
// models that fit it (the recommended one chosen), each workspace ticked or not with what Use will
// do to it and what it takes, the download, and Use, Retry after a step that failed, or Close.
Dialog {
    closeOnQ: true
    maxWidthPercent: 95
    maxHeightPercent: 90
    title: qsTrId("autodoc.dialog.embeddingSetup.set_up_semantic_search")
    width: 110
    dim: true
    Flex {
        direction: Tui.Vertical
        Text { text: qsTrId("autodoc.dialog.embeddingSetup.ollama") }
        TextField { id: eBase; text: App.embedSetupBase }
        Text { wrapMode: Tui.WordWrap; text: App.embedSetupHost }
        Text { visible: App.embedSetupReady; text: qsTrId("autodoc.dialog.embeddingSetup.model") }
        ComboBox { visible: App.embedSetupReady; model: App.embedSetupModels; textRole: "label"; currentIndex: App.embedSetupModelIndex; onActivated: App.embedSetupModelPicked(index) }
        Text { visible: App.embedSetupReady; wrapMode: Tui.WordWrap; text: qsTrId("autodoc.dialog.embeddingSetup.enter_ticks_a_workspace_or_unticks_it") }
        TableView {
            id: eRows
            visible: App.embedSetupReady
            palette.highlight: Theme.document.highlight
            palette.highlightedText: Theme.document.highlightedText
            model: App.embedSetupRows
            Layout.fillHeight: true
            onActivated: App.embedSetupToggle(index)
            TableViewColumn { role: "mark"; title: ""; width: 4 }
            TableViewColumn { role: "workspace"; title: "WORKSPACE"; width: 18 }
            TableViewColumn { role: "now"; title: "NOW"; width: 16 }
            TableViewColumn { role: "will"; title: "USE WILL"; width: 0 }
            TableViewColumn { role: "texts"; title: "TEXTS"; width: 8 }
            TableViewColumn { role: "room"; title: "ROOM"; width: 10 }
            TableViewColumn { role: "time"; title: "TIME"; width: 10 }
        }
        Text { wrapMode: Tui.WordWrap; text: App.embedSetupStatus }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.embeddingSetup.check"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.embedSetupCheck(eBase.text) }
        Button { text: qsTrId("autodoc.dialog.embeddingSetup.download"); visible: App.embedSetupPullShown; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.embedSetupPull() }
        Button { text: qsTrId("autodoc.dialog.embeddingSetup.stop_download"); visible: App.embedSetupStopShown; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.embedSetupStop() }
        Button { text: qsTrId("autodoc.dialog.embeddingSetup.use"); visible: App.embedSetupUseShown; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.embedSetupUse() }
        Button { text: qsTrId("autodoc.dialog.embeddingSetup.retry"); visible: App.embedSetupRetryShown; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.embedSetupRetry() }
        Button { text: qsTrId("autodoc.dialog.embeddingSetup.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onRejected: App.embedSetupClosed()
}
