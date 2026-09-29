// Preferences.qml — Options › Editor preferences… (SPC ,): the editor and the screen, kept in the
// daemon's store so they are the same whichever workspace is open. Each chooser applies at once;
// the page's width applies as it is typed, once it is a width. The AI models are their own dialog
// (AIModels.qml).
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 90
    title: "editor preferences"
    width: 90
    dim: false
    helpText: App.prefsError
    // two columns: the editor on the left, the screen around it on the right
    Flex {
        direction: Tui.Horizontal
        Flex {
            direction: Tui.Vertical
            Layout.fillWidth: true
            Text { text: "keymap" }
            ComboBox { model: App.keymaps; textRole: "label"; currentIndex: App.keymapIndex; onActivated: App.setKeymapIndex(index) }
            Text { text: "the page's width, in columns (40 to 400)" }
            TextField { id: ruler; text: App.rulerText; onTextEdited: App.setRuler(text); onAccepted: App.setRuler(text) }
            Text { text: "theme" }
            ComboBox { model: App.themes; textRole: "label"; currentIndex: App.themeIndex; onActivated: App.setThemeIndex(index) }
        }
        Flex {
            direction: Tui.Vertical
            Layout.fillWidth: true
            Text { text: "hide the menu bar (F10 or Alt+letter brings it up)" }
            ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.menuHiddenIndex; onActivated: App.setMenuHiddenIndex(index) }
            Text { text: "show the status line" }
            ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.statusShownIndex; onActivated: App.setStatusShownIndex(index) }
            Text { text: "the explorer opens from" }
            ComboBox { model: App.edges; textRole: "label"; currentIndex: App.explorerEdgeIndex; onActivated: App.setExplorerEdge(index) }
            Text { text: "the links open from" }
            ComboBox { model: App.edges; textRole: "label"; currentIndex: App.linksEdgeIndex; onActivated: App.setLinksEdge(index) }
        }
    }
    DialogButtonBox {
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
