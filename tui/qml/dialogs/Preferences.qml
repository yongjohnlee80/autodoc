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
    // two sections, each framed and titled: the editor on the left, the screen around it on the
    // right; a column apart, a column inside each border, and a row between the settings
    Flex {
        direction: Tui.Horizontal
        Frame {
            title: "editor"
            Layout.fillWidth: true
            Flex {
                direction: Tui.Horizontal
                Text { text: " " }
                Flex {
                    direction: Tui.Vertical
                    Layout.fillWidth: true
                    Text { text: "editor mode" }
                    ComboBox { model: App.keymaps; textRole: "label"; currentIndex: App.keymapIndex; onActivated: App.setKeymapIndex(index) }
                    Text { text: "" }
                    Text { text: "page width, in columns (40–400)" }
                    TextField { id: ruler; text: App.rulerText; onTextEdited: App.setRuler(text); onAccepted: App.setRuler(text) }
                    Text { text: "" }
                    Text { text: "theme" }
                    ComboBox { model: App.themes; textRole: "label"; currentIndex: App.themeIndex; onActivated: App.setThemeIndex(index) }
                    Text { text: "" }
                    Text { text: "wrap long lines" }
                    ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.wrapIndex; onActivated: App.setWrapIndex(index) }
                    Text { text: "" }
                    Text { text: "line numbers" }
                    ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.lineNumbersIndex; onActivated: App.setLineNumbersIndex(index) }
                }
                Text { text: " " }
            }
        }
        Text { text: " " }
        Frame {
            title: "screen"
            Layout.fillWidth: true
            Flex {
                direction: Tui.Horizontal
                Text { text: " " }
                Flex {
                    direction: Tui.Vertical
                    Layout.fillWidth: true
                    Text { text: "hide the menu bar (F10 shows it)" }
                    ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.menuHiddenIndex; onActivated: App.setMenuHiddenIndex(index) }
                    Text { text: "" }
                    Text { text: "show the status line" }
                    ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.statusShownIndex; onActivated: App.setStatusShownIndex(index) }
                    Text { text: "" }
                    Text { text: "the explorer opens from" }
                    ComboBox { model: App.edges; textRole: "label"; currentIndex: App.explorerEdgeIndex; onActivated: App.setExplorerEdge(index) }
                    Text { text: "" }
                    Text { text: "the links open from" }
                    ComboBox { model: App.edges; textRole: "label"; currentIndex: App.linksEdgeIndex; onActivated: App.setLinksEdge(index) }
                    Text { text: "" }
                    Text { text: "notifications in the" }
                    ComboBox { model: App.corners; textRole: "label"; currentIndex: App.toastCornerIndex; onActivated: App.setToastCorner(index) }
                    Text { text: "" }
                    Text { text: "a notification stays" }
                    ComboBox { model: App.toastSeconds; textRole: "label"; currentIndex: App.toastSecondsIndex; onActivated: App.setToastSeconds(index) }
                }
                Text { text: " " }
            }
        }
    }
    DialogButtonBox {
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
