// AIModels.qml — System › AI models… (SPC a), in two tabs. Embedding Models: the providers semantic
// search can use. Ranker Models: the rankers search's second stage can use (ADR 0215), and the
// window of top candidates the one in use ranks. On each, the list and which is in use on the left;
// on the right, the one under the cursor's usage by day and its latest calls. Add…, Edit…, Use and
// Remove… act on the tab that is current; each tab's own buttons show only on it.
Dialog {
    closeOnQ: true
    maxWidthPercent: 94
    maxHeightPercent: 92
    title: "AI models"
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
                    title: "usage and calls"
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
                    title: "rankers"
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
                    title: "usage and calls"
                    Text { text: App.rankerDetail; wrapMode: Tui.WordWrap }
                }
            }
        }
    }
    DialogButtonBox {
        Button { text: "&Add…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.aiAdd() }
        Button { text: "&Edit…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.aiEdit(providerTable.currentIndex) }
        Button { text: "&Use"; enabled: App.aiUseEnabled; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.aiUse(providerTable.currentIndex) }
        Button { text: "&Words only"; visible: App.aiEmbeddingTab; enabled: App.aiEmbeddingTab; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.stopSemantic() }
        Button { text: "Cancel &indexing"; visible: App.aiEmbeddingTab; enabled: App.aiEmbeddingTab; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.cancelIndexing() }
        Button { text: "&Vectors…"; visible: App.aiEmbeddingTab; enabled: App.aiEmbeddingTab; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.openVectors() }
        Button { text: "&Don't use"; visible: App.aiRankerTab; enabled: App.aiRankerChoice; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.stopRanking() }
        Button { text: "Wi&ndow…"; visible: App.aiRankerTab; enabled: App.aiRankerChoice; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startRankerWindow() }
        Button { text: "&Remove…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.aiRemove(providerTable.currentIndex) }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
