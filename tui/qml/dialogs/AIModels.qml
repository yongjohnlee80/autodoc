// AIModels.qml — Options › AI models… (SPC a): the embedding providers semantic search can use.
// On the left, the providers and which is in use; on the right, the one under the cursor's usage
// by day and its latest calls. Add… and Edit… open the provider form over it.
Dialog {
    closeOnQ: true
    maxWidthPercent: 94
    maxHeightPercent: 92
    title: "AI models"
    dim: false
    Split {
        orientation: Tui.Horizontal
        ratio: 0.5
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
                    TableViewColumn { role: "name"; title: "NAME"; width: 14 }
                    TableViewColumn { role: "kind"; title: "KIND"; width: 17 }
                    TableViewColumn { role: "model"; title: "MODEL"; width: 0 }
                    TableViewColumn { role: "context"; title: "CONTEXT"; width: 8 }
                    TableViewColumn { role: "apiKey"; title: "KEY"; width: 7 }
                }
                // semantic search's state, as the status line shows it
                Flex {
                    direction: Tui.Horizontal
                    Text { text: App.semanticMark; color: App.semanticDot }
                    Text { text: " " }
                    Text { text: App.semanticLabel }
                }
                Text { text: App.providersStatus; wrapMode: Tui.WordWrap }
            }
        }
        Frame {
            title: "usage and calls"
            Text { text: App.providerDetail; wrapMode: Tui.WordWrap }
        }
    }
    DialogButtonBox {
        Button { text: "&Add…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startAddProvider() }
        Button { text: "&Edit…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startEditProvider(providerTable.currentIndex) }
        Button { text: "&Use"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.useProvider(providerTable.currentIndex) }
        Button { text: "&Words only"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.stopSemantic() }
        Button { text: "&Remove…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startRemoveProvider(providerTable.currentIndex) }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
