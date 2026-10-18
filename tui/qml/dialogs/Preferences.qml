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
    // right; a column apart, a column inside each border. One setting a row: its label in a column
    // of its own (Layout.preferredWidth), its chooser beside it, a blank row between groups
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
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "editor mode"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.keymaps; textRole: "label"; currentIndex: App.keymapIndex; onActivated: App.setKeymapIndex(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "page width (40–400)"; Layout.preferredWidth: 21; toolTipText: "the page's width, in columns: 40 to 400" }
                        TextField { id: ruler; text: App.rulerText; onTextEdited: App.setRuler(text); onAccepted: App.setRuler(text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "theme"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.themes; textRole: "label"; currentIndex: App.themeIndex; onActivated: App.setThemeIndex(index) }
                    }
                    Text { text: "" }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "wrap long lines"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.wrapIndex; onActivated: App.setWrapIndex(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "line numbers"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.lineNumbersIndex; onActivated: App.setLineNumbersIndex(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "indentation marks"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.indentMarkModes; textRole: "label"; currentIndex: App.indentMarksIndex; onActivated: App.setIndentMarksIndex(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "image previews"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.imagesIndex; onActivated: App.setImagesIndex(index) }
                    }
                    // a document with two views (HTML): its source raw, or its text simplified
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "two-view documents"; Layout.preferredWidth: 21; toolTipText: "how a document with two views (HTML) opens: asked each time, raw (its source, to edit) or simplified (its text)" }
                        ComboBox { model: App.openAsChoices; textRole: "label"; currentIndex: App.openAsIndex; onActivated: App.setOpenAsIndex(index) }
                    }
                    // an open file changed on disk with no unsaved edits: reloaded at once, or asked about
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "files changed on disk"; Layout.preferredWidth: 21; toolTipText: "an open file another program changed, with no unsaved edits: reloaded at once (one undo step), or asked about as a modified one is" }
                        ComboBox { model: App.diskReloadChoices; textRole: "label"; currentIndex: App.diskReloadIndex; onActivated: App.setDiskReloadIndex(index) }
                    }
                    Text { text: "" }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "terminal opens from"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.terminalEdges; textRole: "label"; currentIndex: App.terminalEdgeIndex; onActivated: App.setTerminalEdge(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "terminal size"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.terminalSizes; textRole: "label"; currentIndex: App.terminalSizeIndex; onActivated: App.setTerminalSize(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "terminal length"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.terminalLengths; textRole: "label"; currentIndex: App.terminalLengthIndex; onActivated: App.setTerminalLength(index) }
                    }
                    // the theme is the terminal's and the agents' shells' (terminal.go): their text, background
                    // and cursor; a program's own 16 colours stay its own
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "terminal theme"; Layout.preferredWidth: 21; toolTipText: "The terminal's and the agents' shells: their text, background and cursor. A program's own 16 colours (ls's, a prompt's) stay its own." }
                        ComboBox { toolTipText: "The terminal's and the agents' shells: their text, background and cursor. A program's own 16 colours (ls's, a prompt's) stay its own."; model: App.terminalThemes; textRole: "label"; currentIndex: App.terminalThemeIndex; onActivated: App.setTerminalTheme(index) }
                    }
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
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "hide the menu bar"; Layout.preferredWidth: 21; toolTipText: "F10 shows it while hidden" }
                        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.menuHiddenIndex; onActivated: App.setMenuHiddenIndex(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "show the status line"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.statusShownIndex; onActivated: App.setStatusShownIndex(index) }
                    }
                    Text { text: "" }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "explorer opens from"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.edges; textRole: "label"; currentIndex: App.explorerEdgeIndex; onActivated: App.setExplorerEdge(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "links open from"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.edges; textRole: "label"; currentIndex: App.relationsEdgeIndex; onActivated: App.setRelationsEdge(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "todos open from"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.edges; textRole: "label"; currentIndex: App.todosEdgeIndex; onActivated: App.setTodosEdge(index) }
                    }
                    Text { text: "" }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "agent opens from"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.agentEdges; textRole: "label"; currentIndex: App.agentEdgeIndex; onActivated: App.setAgentEdge(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "agent size"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.agentSizes; textRole: "label"; currentIndex: App.agentSizeIndex; onActivated: App.setAgentSize(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "agent length"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.agentLengths; textRole: "label"; currentIndex: App.agentLengthIndex; onActivated: App.setAgentLength(index) }
                    }
                    Text { text: "" }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "notifications in the"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.corners; textRole: "label"; currentIndex: App.toastCornerIndex; onActivated: App.setToastCorner(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "a notification stays"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.toastSeconds; textRole: "label"; currentIndex: App.toastSecondsIndex; onActivated: App.setToastSeconds(index) }
                    }
                    Text { text: "" }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "About at startup"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.aboutAtStartIndex; onActivated: App.setAboutAtStartIndex(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "look for updates"; Layout.preferredWidth: 21 }
                        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.updateCheckIndex; onActivated: App.setUpdateCheckIndex(index) }
                    }
                }
                Text { text: " " }
            }
        }
    }
    DialogButtonBox {
        Button { text: "&File types…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.openActiveSettings() }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
