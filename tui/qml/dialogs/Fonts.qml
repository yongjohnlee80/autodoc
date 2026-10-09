// Fonts.qml — Options › Fonts and zoom… (the GUI's): the cell font everything on the grid is drawn
// in and its size, the prose font of the Rendered view and the HTML preview, and the zoom over all
// of them (fonts.go). Each chooser applies at once, as Preferences' do; the store keeps them.
Dialog {
    closeOnQ: true
    title: "fonts and zoom"
    width: 60
    dim: false
    helpText: "each applies at once · Esc closes"
    Flex {
        direction: Tui.Vertical
        Text { text: "the cell font (monospace): the page, the panels, the menus" }
        ComboBox { model: App.cellFonts; textRole: "label"; currentIndex: App.cellFontIndex; onActivated: App.setCellFontIndex(index) }
        Text { text: "" }
        Text { text: "its size" }
        ComboBox { model: App.fontSizes; textRole: "label"; currentIndex: App.fontSizeIndex; onActivated: App.setFontSizeIndex(index) }
        Text { text: "" }
        Text { text: "the prose font: the Rendered view (Ctrl+T) and the HTML preview" }
        ComboBox { model: App.proseFonts; textRole: "label"; currentIndex: App.proseFontIndex; onActivated: App.setProseFontIndex(index) }
        Text { text: "" }
        Text { text: "zoom (Ctrl+= and Ctrl+- step it, Ctrl+0 resets it)" }
        ComboBox { model: App.zoomSteps; textRole: "label"; currentIndex: App.zoomIndex; onActivated: App.setZoomIndex(index) }
    }
}
