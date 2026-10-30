// AIModels.qml — System › Search models… (SPC s), in two tabs. Embedding Models: the providers semantic
// search can use. Ranker Models: the rankers search's second stage can use
// (docs/decisions/search.md#re-ranking-with-one-global-ranker), and the window of top candidates
// the one in use ranks. On each, the list and which is in use on the left; on the right, the one
// under the cursor's usage by day and its latest calls. Add…, Edit…, Use and Remove… act on the tab
// that is current; each tab's own buttons show only on it.
Dialog {
    closeOnQ: true
    maxWidthPercent: 94
    maxHeightPercent: 92
    title: qsTrId("autodoc.dialog.aIModels.search_models")
    dim: false
    TabView {
        id: aiTabs
        currentIndex: App.aiTab
        onCurrentIndexChanged: App.aiTabMoved(index)
        Tab {
            title: "Embedding Models"
            Split {
                orientation: Tui.Horizontal
                ratio: 0.6
                Frame {
                    title: App.providersTitle
                    Flex {
                        direction: Tui.Vertical
                        TableView {
                            id: providerTable
                            palette.highlight: Theme.document.highlight
                            palette.highlightedText: Theme.document.highlightedText
                            model: App.providers
                            Layout.fillHeight: true
                            onCurrentIndexChanged: App.showProvider(index)
                            onActivated: App.startEditProvider(index)
                            TableViewColumn { role: "use"; title: ""; width: 2 }
                            TableViewColumn { role: "name"; title: "NAME"; width: 12 }
                            TableViewColumn { role: "kind"; title: "KIND"; width: 15 }
                            TableViewColumn { role: "model"; title: "MODEL"; width: 0 }
                            TableViewColumn { role: "context"; title: "CONTEXT"; width: 7 }
                            TableViewColumn { role: "apiKey"; title: "KEY"; width: 6 }
                        }
                        // semantic search's state, as the status line shows it
                        Flex {
                            direction: Tui.Horizontal
                            Text { text: App.semanticMark; color: App.semanticDot }
                            Text { text: " " }
                            Text { text: App.semanticDetail }
                        }
                        Text { text: App.providersStatus; wrapMode: Tui.WordWrap }
                    }
                }
                Frame {
                    title: qsTrId("autodoc.dialog.aIModels.usage_and_calls")
                    Text { text: App.providerDetail; wrapMode: Tui.WordWrap }
                }
            }
        }
        Tab {
            title: "Ranker Models"
            Split {
                orientation: Tui.Horizontal
                ratio: 0.6
                Frame {
                    title: qsTrId("autodoc.dialog.aIModels.rankers")
                    Flex {
                        direction: Tui.Vertical
                        TableView {
                            id: rankerTable
                            palette.highlight: Theme.document.highlight
                            palette.highlightedText: Theme.document.highlightedText
                            model: App.rankers
                            Layout.fillHeight: true
                            onCurrentIndexChanged: App.showRanker(index)
                            onActivated: App.startEditRanker(index)
                            TableViewColumn { role: "use"; title: ""; width: 2 }
                            TableViewColumn { role: "name"; title: "NAME"; width: 12 }
                            TableViewColumn { role: "kind"; title: "KIND"; width: 12 }
                            TableViewColumn { role: "model"; title: "MODEL"; width: 0 }
                            TableViewColumn { role: "apiKey"; title: "KEY"; width: 6 }
                        }
                        Text { text: App.rankersStatus; wrapMode: Tui.WordWrap }
                    }
                }
                Frame {
                    title: qsTrId("autodoc.dialog.aIModels.usage_and_calls")
                    Text { text: App.rankerDetail; wrapMode: Tui.WordWrap }
                }
            }
        }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.aIModels.set_up"); visible: App.aiEmbeddingTab; enabled: App.aiEmbeddingTab; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.openEmbeddingSetup() }
        Button { text: qsTrId("autodoc.dialog.aIModels.add"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.aiAdd() }
        Button { text: qsTrId("autodoc.dialog.aIModels.edit"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.aiEdit(providerTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.aIModels.use"); enabled: App.aiUseEnabled; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.aiUse(providerTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.aIModels.words_only"); visible: App.aiEmbeddingTab; enabled: App.aiEmbeddingTab; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.stopProvider() }
        Button { text: qsTrId("autodoc.dialog.aIModels.cancel_indexing"); visible: App.aiEmbeddingTab; enabled: App.aiEmbeddingTab; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.cancelIndexing() }
        Button { text: qsTrId("autodoc.dialog.aIModels.vectors"); visible: App.aiEmbeddingTab; enabled: App.aiEmbeddingTab; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.openVectors() }
        Button { text: qsTrId("autodoc.dialog.aIModels.don_t_use"); visible: App.aiRankerTab; enabled: App.aiRankerChoice; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.stopRanker() }
        Button { text: qsTrId("autodoc.dialog.aIModels.window"); visible: App.aiRankerTab; enabled: App.aiRankerChoice; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startRankerWindow() }
        Button { text: qsTrId("autodoc.dialog.aIModels.remove"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.aiRemove(providerTable.currentIndex) }
        Button { text: qsTrId("autodoc.dialog.aIModels.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
