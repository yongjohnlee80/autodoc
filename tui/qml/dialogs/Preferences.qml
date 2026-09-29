// Preferences.qml — File › Preferences (SPC ,): how the screen looks, kept in the daemon's store
// so it is the same whichever workspace is open, and the embedding providers semantic search can
// use. Each chooser applies at once.
Dialog {
    closeOnQ: true
    maxWidthPercent: 92
    maxHeightPercent: 92
    title: "preferences"
    dim: false
    helpText: App.prefsError
    Flex {
        direction: Tui.Vertical
        // two columns of choosers, as tall as they are (a Split would take the dialog's height)
        Flex {
            direction: Tui.Horizontal
            Flex {
                direction: Tui.Vertical
                Layout.fillWidth: true
                Text { text: "theme" }
                ComboBox { model: App.themes; textRole: "label"; currentIndex: App.themeIndex; onActivated: App.setThemeIndex(index) }
                Text { text: "hide the menu bar (F10 or Alt+letter brings it up)" }
                ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.menuHiddenIndex; onActivated: App.setMenuHiddenIndex(index) }
                Text { text: "show the status line" }
                ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.statusShownIndex; onActivated: App.setStatusShownIndex(index) }
            }
            Flex {
                direction: Tui.Vertical
                Layout.fillWidth: true
                Text { text: "the explorer opens from" }
                ComboBox { model: App.edges; textRole: "label"; currentIndex: App.explorerEdgeIndex; onActivated: App.setExplorerEdge(index) }
                Text { text: "the links open from" }
                ComboBox { model: App.edges; textRole: "label"; currentIndex: App.linksEdgeIndex; onActivated: App.setLinksEdge(index) }
                Text { text: "the page's width, in columns (Enter applies it)" }
                TextField { id: ruler; text: App.rulerText; onAccepted: App.setRuler(text) }
            }
        }
        Frame {
            title: App.providersTitle
            Layout.fillHeight: true
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
                    TableViewColumn { role: "name"; title: "NAME"; width: 16 }
                    TableViewColumn { role: "kind"; title: "KIND"; width: 18 }
                    TableViewColumn { role: "model"; title: "MODEL"; width: 0 }
                    TableViewColumn { role: "apiKey"; title: "KEY"; width: 7 }
                }
                Text { text: App.providerDetail; wrapMode: Tui.WordWrap }
            }
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
