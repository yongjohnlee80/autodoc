// TerminalLook.qml — Theme and font…, from the terminal's or the agents' tab menu (terminallook.go):
// the terminals' theme and font, the same settings Preferences' terminal row and Options › Fonts
// and zoom… set. A choice shows at once; OK keeps it, Esc puts back what was chosen before.
Dialog {
    closeOnQ: true
    title: "the terminals' theme and font"
    width: 60
    dim: false
    standardButtons: Dialog.Ok | Dialog.Cancel
    defaultButton: Dialog.Ok
    Flex {
        direction: Tui.Vertical
        Text { text: "theme: their text, background and cursor"; toolTipText: "The terminal's and the agents' shells. A program's own 16 colours (ls's, a prompt's) stay its own." }
        ComboBox { model: App.terminalThemes; textRole: "label"; currentIndex: App.terminalThemeIndex; onActivated: App.setTerminalTheme(index) }
        Text { text: "" }
        Text { text: "font: at the cell size"; visible: App.gui }
        ComboBox { visible: App.gui; model: App.terminalFonts; textRole: "label"; currentIndex: App.terminalFontIndex; onActivated: App.setTerminalFontIndex(index) }
        Text { text: App.terminalLookNote; wrapMode: Tui.WordWrap }
    }
    onAccepted: App.terminalLookKept()
    onRejected: App.terminalLookCancelled()
}
