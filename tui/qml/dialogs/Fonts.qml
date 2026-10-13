// Fonts.qml — Options › Fonts and zoom… (the GUI's): the cell font everything on the grid is drawn
// in and its size, the prose font of the Rendered view and the HTML preview, and the zoom over all
// of them (fonts.go). Each chooser applies at once, as Preferences' do; the store keeps them.
Dialog {
    closeOnQ: true
    title: "fonts and zoom"
    width: 60
    dim: false
    helpText: "each applies at once · Esc closes"
    // each description, clipped when the dialog is narrow, is its row's tooltip too: the pointer
    // resting on it, or the keyboard on its chooser, shows it whole
    Flex {
        direction: Tui.Vertical
        Text { text: "the cell font (monospace): the page, the panels, the menus"; toolTipText: "the cell font (monospace): the page, the panels, the menus" }
        ComboBox { toolTipText: "the cell font (monospace): the page, the panels, the menus"; model: App.cellFonts; textRole: "label"; currentIndex: App.cellFontIndex; onActivated: App.setCellFontIndex(index) }
        Text { text: "" }
        Text { text: "the terminals' font: the terminal's and the agents' shells, at the cell size"; toolTipText: "the terminals' font: the terminal's and the agents' shells, at the cell size" }
        ComboBox { toolTipText: "the terminals' font: the terminal's and the agents' shells, at the cell size"; model: App.terminalFonts; textRole: "label"; currentIndex: App.terminalFontIndex; onActivated: App.setTerminalFontIndex(index) }
        Text { text: "" }
        Text { text: "its size" }
        ComboBox { model: App.fontSizes; textRole: "label"; currentIndex: App.fontSizeIndex; onActivated: App.setFontSizeIndex(index) }
        Text { text: "" }
        Text { text: "the prose font: the Rendered view (Ctrl+T) and the HTML preview"; toolTipText: "the prose font: the Rendered view (Ctrl+T) and the HTML preview" }
        ComboBox { toolTipText: "the prose font: the Rendered view (Ctrl+T) and the HTML preview"; model: App.proseFonts; textRole: "label"; currentIndex: App.proseFontIndex; onActivated: App.setProseFontIndex(index) }
        Text { text: "" }
        Text { text: "zoom (Ctrl+= and Ctrl+- step it, Ctrl+0 resets it)"; toolTipText: "zoom (Ctrl+= and Ctrl+- step it, Ctrl+0 resets it)" }
        ComboBox { toolTipText: "zoom (Ctrl+= and Ctrl+- step it, Ctrl+0 resets it)"; model: App.zoomSteps; textRole: "label"; currentIndex: App.zoomIndex; onActivated: App.setZoomIndex(index) }
    }
}
