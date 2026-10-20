// Fonts.qml — Options › Fonts and zoom… (the GUI's): the cell font everything on the grid is drawn
// in and its size, the prose font of the Rendered view and the HTML preview, and the zoom over all
// of them (fonts.go). Each chooser applies at once, as Preferences' do; the store keeps them.
Dialog {
    closeOnQ: true
    title: qsTrId("autodoc.dialog.fonts.fonts_and_zoom")
    width: 60
    dim: false
    helpText: qsTrId("autodoc.dialog.fonts.each_applies_at_once_esc_closes")
    // each description, clipped when the dialog is narrow, is its row's tooltip too: the pointer
    // resting on it, or the keyboard on its chooser, shows it whole
    Flex {
        direction: Tui.Vertical
        Text { text: qsTrId("autodoc.dialog.fonts.the_cell_font_monospace_the_page_the_panels_the"); toolTipText: "the cell font (monospace): the page, the panels, the menus" }
        ComboBox { toolTipText: "the cell font (monospace): the page, the panels, the menus"; model: App.cellFonts; textRole: "label"; currentIndex: App.cellFontIndex; onActivated: App.setCellFontIndex(index) }
        Text { text: "" }
        Text { text: qsTrId("autodoc.dialog.fonts.the_terminals_font_the_terminal_s_and_the_agents"); toolTipText: "the terminals' font: the terminal's and the agents' shells, at the cell size" }
        ComboBox { toolTipText: "the terminals' font: the terminal's and the agents' shells, at the cell size"; model: App.terminalFonts; textRole: "label"; currentIndex: App.terminalFontIndex; onActivated: App.setTerminalFontIndex(index) }
        Text { text: "" }
        Text { text: qsTrId("autodoc.dialog.fonts.its_size") }
        ComboBox { model: App.fontSizes; textRole: "label"; currentIndex: App.fontSizeIndex; onActivated: App.setFontSizeIndex(index) }
        Text { text: "" }
        Text { text: qsTrId("autodoc.dialog.fonts.the_agents_text_size_the_agent_panel_s_shells_a"); toolTipText: "the agents' text size: the agent panel's shells, a share of the cell size (zoom moves it too)" }
        ComboBox { toolTipText: "the agents' text size: the agent panel's shells, a share of the cell size (zoom moves it too)"; model: App.agentScales; textRole: "label"; currentIndex: App.agentScaleIndex; onActivated: App.setAgentScaleIndex(index) }
        Text { text: "" }
        Text { text: qsTrId("autodoc.dialog.fonts.the_prose_font_the_rendered_view_ctrl_t_and_the"); toolTipText: "the prose font: the Rendered view (Ctrl+T) and the HTML preview" }
        ComboBox { toolTipText: "the prose font: the Rendered view (Ctrl+T) and the HTML preview"; model: App.proseFonts; textRole: "label"; currentIndex: App.proseFontIndex; onActivated: App.setProseFontIndex(index) }
        Text { text: "" }
        Text { text: qsTrId("autodoc.dialog.fonts.zoom_ctrl_and_ctrl_step_it_ctrl_0_resets_it"); toolTipText: "zoom (Ctrl+= and Ctrl+- step it, Ctrl+0 resets it)" }
        ComboBox { toolTipText: "zoom (Ctrl+= and Ctrl+- step it, Ctrl+0 resets it)"; model: App.zoomSteps; textRole: "label"; currentIndex: App.zoomIndex; onActivated: App.setZoomIndex(index) }
    }
}
