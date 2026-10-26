// main.qml — AutoDoc's screen, in QML.
//
// STRUCTURE ONLY: what is on the screen, where it is docked, and what each control triggers. It
// names no colour (the imported theme does) and runs nothing itself: every action is an App
// command, and the host decides what it does.
//
// A writing screen: the file is a page, as wide as reading wants (App.pageWidth, the ruler's
// columns and its border) and centred, and nothing else shows until it is asked for. The menu bar
// hides until F10 or an Alt+letter brings it up; the status line shows when its preference says;
// the explorer and the relations are drawers over the page, which never moves under them.

import tui 1.0
import autodoc 1.0                // App: this program's state and commands
import autodoc.theme.dark 1.0     // the Theme singleton; View › Theme switches it
import autodoc.dialogs 1.0        // the pickers, the file, workspace, preference and quit dialogs
import autodoc.views 1.0          // Help, About, the leader card
import tui.panels 1.0              // Panel, the docked panels (golib, ADR 1792459145)

Window {
    // below this, the screen asks to be enlarged, with Quit: a terminal's classic width, and a
    // short pane's height
    minimumWidth: 80
    minimumHeight: 20
    color: Theme.app.backdrop       // the screen around the page: a dimmed tone of the page's own
    palette.window: Theme.app.window
    palette.windowText: Theme.app.windowText
    palette.button: Theme.app.button
    palette.buttonText: Theme.app.buttonText
    palette.highlight: Theme.app.highlight
    palette.accent: Theme.menu.accent    // the access keys' colour: the menu bar's, and the dialogs' buttons'
    palette.highlightedText: Theme.app.highlightedText
    palette.base: Theme.app.base
    palette.text: Theme.app.text
    palette.inactive.highlight: Theme.app.inactive.highlight
    palette.inactive.highlightedText: Theme.app.inactive.highlightedText
    palette.mid: Theme.app.mid
    palette.light: Theme.app.light

    // Markdown's colours, set once here and inherited by the page's highlighter and every preview
    syntax.keyword: Theme.syntax.keyword
    syntax.controlFlow: Theme.syntax.controlFlow
    syntax.dataType: Theme.syntax.dataType
    syntax.attribute: Theme.syntax.attribute
    syntax.function: Theme.syntax.function
    syntax.string: Theme.syntax.string
    syntax.specialChar: Theme.syntax.specialChar
    syntax.decVal: Theme.syntax.decVal
    syntax.float: Theme.syntax.float
    syntax.baseN: Theme.syntax.baseN
    syntax.constant: Theme.syntax.constant
    syntax.comment: Theme.syntax.comment
    syntax.alert: Theme.syntax.alert
    syntax.import: Theme.syntax.import
    syntax.operator: Theme.syntax.operator

    // the Window's keys fire whichever widget has focus, when it leaves them (tui/keys.go)
    // ---- the Window's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
    Shortcut { sequence: keyId("autodoc.window.open.leader"); onActivated: leader.open() }
    Shortcut { sequence: keyId("autodoc.window.movePane.h"); onActivated: App.movePane("h") }
    Shortcut { sequence: keyId("autodoc.window.movePane.j"); onActivated: App.movePane("j") }
    Shortcut { sequence: keyId("autodoc.window.movePane.k"); onActivated: App.movePane("k") }
    Shortcut { sequence: keyId("autodoc.window.movePane.l"); onActivated: App.movePane("l") }
    Shortcut { sequence: keyId("autodoc.window.save"); onActivated: App.save() }
    Shortcut { sequence: keyId("autodoc.window.newFile"); onActivated: App.newFile() }
    Shortcut { sequence: keyId("autodoc.window.openSearch"); onActivated: App.openSearch() }
    Shortcut { sequence: keyId("autodoc.window.openCommandLine"); onActivated: App.openCommandLine() }
    Shortcut { sequence: keyId("autodoc.window.openFind"); onActivated: App.openFind() }
    Shortcut { sequence: keyId("autodoc.window.findNext"); onActivated: App.findNext() }
    Shortcut { sequence: keyId("autodoc.window.findPrevious"); onActivated: App.findPrevious() }
    Shortcut { sequence: keyId("autodoc.window.pickWorkspace"); onActivated: App.pickWorkspace() }
    Shortcut { sequence: keyId("autodoc.window.quit"); onActivated: App.quit() }
    Shortcut { sequence: keyId("autodoc.window.maximizePanel"); onActivated: App.maximizePanel() }
    Shortcut { sequence: keyId("autodoc.window.nextPanelTab"); onActivated: App.nextPanelTab() }
    Shortcut { sequence: keyId("autodoc.window.previousPanelTab"); onActivated: App.previousPanelTab() }
    Shortcut { sequence: keyId("autodoc.window.cycleWidget"); onActivated: App.cycleWidget() }
    Shortcut { sequence: keyId("autodoc.window.cycleWidgetBack"); onActivated: App.cycleWidgetBack() }
    Shortcut { sequence: keyId("autodoc.window.help.open"); onActivated: help.open() }
    Shortcut { sequence: keyId("autodoc.window.zoomWindow.in"); onActivated: App.zoomWindow("in") }
    Shortcut { sequence: keyId("autodoc.window.zoomWindow.out"); onActivated: App.zoomWindow("out") }
    Shortcut { sequence: keyId("autodoc.window.setZoom.100"); onActivated: App.setZoom(100) }
    Shortcut { sequence: keyId("autodoc.window.toggleVimKeys"); onActivated: App.toggleVimKeys() }
    // ---- end of the generated keys ----

    MenuBar {
        id: menuBar
        Dock.edge: Tui.Top
        autoHide: App.menuAutoHide
        vimNavigation: true
        palette.window: Theme.menu.window
        palette.windowText: Theme.menu.windowText
        palette.highlight: Theme.menu.highlight
        palette.highlightedText: Theme.menu.highlightedText
        palette.accent: Theme.menu.accent

        Menu {
            title: qsTrId("autodoc.main.file")
            MenuItem { text: qsTrId("autodoc.main.new_file"); onTriggered: App.newFile() }
            MenuItem { text: qsTrId("autodoc.main.open_document"); onTriggered: App.openPicker() }
            MenuItem { text: qsTrId("autodoc.main.open_file"); onTriggered: App.openFileDialog() }
            MenuItem { text: qsTrId("autodoc.main.recent_files"); onTriggered: App.openRecent() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.save"); onTriggered: App.save() }
            MenuItem { text: qsTrId("autodoc.main.reload_from_disk"); onTriggered: App.reload() }
            MenuItem { text: qsTrId("autodoc.main.close_buffer"); onTriggered: App.closeBuffer() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.preview_html"); onTriggered: App.previewHTML() }
            // what draws a window's Mermaid diagrams (preview.go); the terminal's are mermaid.js's
            Menu {
                title: qsTrId("autodoc.main.preview_option")
                visible: App.gui
                MenuItem { text: qsTrId("autodoc.main.native_mermaid_engine"); group: "mermaidEngine"; checked: App.mermaidNative; onTriggered: App.setMermaidEngine("native") }
                MenuItem { text: qsTrId("autodoc.main.mermaid_js");            group: "mermaidEngine"; checked: App.mermaidJS;     onTriggered: App.setMermaidEngine("mermaidjs") }
            }
            MenuItem { text: qsTrId("autodoc.main.preview_mermaid_diagram"); onTriggered: App.previewDiagram() }
            MenuItem { text: qsTrId("autodoc.main.open_with_default_app"); onTriggered: App.openWithDefaultApp() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.exit"); onTriggered: App.quit() }
        }
        Menu {
            title: qsTrId("autodoc.main.go")
            MenuItem { text: qsTrId("autodoc.main.search"); onTriggered: App.openSearch() }
            MenuItem { text: qsTrId("autodoc.main.outline"); onTriggered: App.openOutline() }
            MenuItem { text: qsTrId("autodoc.main.buffers"); onTriggered: App.openBuffers() }
            MenuItem { text: qsTrId("autodoc.main.widget"); onTriggered: App.openWidgets() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.explorer"); onTriggered: App.toggleExplorer() }
            MenuItem { text: qsTrId("autodoc.main.notifications"); onTriggered: App.openNotices() }
            MenuSeparator {}
            // the panels that stay open, checked while they show
            MenuItem { text: qsTrId("autodoc.main.agent"); checkable: true; checked: App.agentShown; onTriggered: App.toggleAgent() }
            MenuItem { text: qsTrId("autodoc.main.terminal"); checkable: true; checked: App.terminalShown; onTriggered: App.toggleTerminal() }
            MenuItem { text: qsTrId("autodoc.main.results"); checkable: true; checked: App.resultsShown; onTriggered: App.toggleResults() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.arrange_panel"); onTriggered: App.arrangePanel() }
            MenuItem { text: qsTrId("autodoc.main.reset_panel_layout"); onTriggered: App.resetPanelLayout() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.workspace"); onTriggered: App.pickWorkspace() }
            MenuItem { text: qsTrId("autodoc.main.manage_workspaces"); onTriggered: App.manageWorkspaces() }
        }
        // the views of the shown document's relations (relations.go)
        Menu {
            title: qsTrId("autodoc.main.relations")
            MenuItem { text: qsTrId("autodoc.main.links_spc_l"); onTriggered: App.toggleRelations() }
            // ---- the relation views' menu items (the GUI's) ----
        }
        // Search: the page's find for the Text mode too, where / types a slash, and the workspace's
        Menu {
            title: qsTrId("autodoc.main.search_2")
            MenuItem { text: qsTrId("autodoc.main.find_in_page"); onTriggered: App.openFindInPage() }
            MenuItem { text: qsTrId("autodoc.main.find_next"); onTriggered: App.findAgainNext() }
            MenuItem { text: qsTrId("autodoc.main.find_previous"); onTriggered: App.findAgainPrevious() }
            MenuItem { text: qsTrId("autodoc.main.clear_find"); onTriggered: App.clearFind() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.search_files"); onTriggered: App.openSearch() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.command_line_ctrl"); onTriggered: App.openCommandLine() }
        }
        Menu {
            title: qsTrId("autodoc.main.view")
            MenuItem { text: qsTrId("autodoc.main.status_line"); checkable: true; checked: App.statusShown; onTriggered: App.toggleStatusLine() }
            MenuItem { text: qsTrId("autodoc.main.hide_the_menu_bar"); checkable: true; checked: App.menuAutoHide; onTriggered: App.toggleMenuBar() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.wrap_long_lines"); checkable: true; checked: App.editorWrap; onTriggered: App.toggleWrap() }
            MenuItem { text: qsTrId("autodoc.main.line_numbers"); checkable: true; checked: App.lineNumbers; onTriggered: App.toggleLineNumbers() }
            Menu {
                title: qsTrId("autodoc.main.indentation_marks")
                MenuItem { text: qsTrId("autodoc.main.1_off");                  group: "indentMarks"; checked: App.indentMarksOff;        onTriggered: App.setIndentMarks("off") }
                MenuItem { text: qsTrId("autodoc.main.2_on_the_cursor_s_line"); group: "indentMarks"; checked: App.indentMarksCursorLine; onTriggered: App.setIndentMarks("cursorLine") }
                MenuItem { text: qsTrId("autodoc.main.3_on_every_line");        group: "indentMarks"; checked: App.indentMarksAll;        onTriggered: App.setIndentMarks("all") }
            }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.image_previews"); checkable: true; checked: App.imagePreviews; onTriggered: App.toggleImagePreviews() }
        }
        // the todo stores, AutoDoc's own view of them (todos.go)
        Menu {
            title: qsTrId("autodoc.main.todos")
            MenuItem { text: qsTrId("autodoc.main.open_todos_spc_x"); onTriggered: App.openTodos() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.new_store"); onTriggered: App.todoNew() }
            MenuItem { text: qsTrId("autodoc.main.load_store"); onTriggered: App.todoLoad() }
        }
        Menu {
            title: qsTrId("autodoc.main.options")
            Menu {
                title: qsTrId("autodoc.main.editor_mode")
                // a shared group makes these a radio set, as editor-qml's are
                MenuItem { text: qsTrId("autodoc.main.1_vim_modal");     group: "keymap"; checked: App.keymapVim;  onTriggered: App.setKeymap("vim") }
                MenuItem { text: qsTrId("autodoc.main.2_text_modeless");  group: "keymap"; checked: App.keymapText; onTriggered: App.setKeymap("text") }
            }
            Menu {
                title: qsTrId("autodoc.main.theme")
                // golib's themes, every one it ships (theme.go: builtinThemeRows), then the user's
                Instantiator {
                    model: App.builtinThemes
                    MenuItem { text: model.label; group: "theme"; checked: model.checked; onTriggered: App.useTheme(model.key) }
                }
                Instantiator {
                    model: App.userThemes
                    MenuItem { text: model.label; enabled: model.enabled; group: "theme"; checked: model.checked; onTriggered: App.useTheme(model.key) }
                }
            }
            Menu {
                title: qsTrId("autodoc.main.language")
                // each in its own script, the same in every language (language.go)
                MenuItem { text: "&1. English";            group: "language"; checked: App.language_en;    onTriggered: App.setLanguage("en") }
                MenuItem { text: "&2. 한국어";             group: "language"; checked: App.language_ko_KR; onTriggered: App.setLanguage("ko_KR") }
                MenuItem { text: "&3. 日本語";             group: "language"; checked: App.language_ja_JP; onTriggered: App.setLanguage("ja_JP") }
                MenuItem { text: "&4. 中文";               group: "language"; checked: App.language_zh_CN; onTriggered: App.setLanguage("zh_CN") }
                MenuItem { text: "&5. Español";            group: "language"; checked: App.language_es;    onTriggered: App.setLanguage("es") }
                MenuItem { text: "&6. Português (Brasil)"; group: "language"; checked: App.language_pt_BR; onTriggered: App.setLanguage("pt_BR") }
            }
            // what draws the window (renderer.go), taken as it opens: the terminal draws in its own
            Menu {
                title: qsTrId("autodoc.main.renderer")
                visible: App.gui
                MenuItem { text: qsTrId("autodoc.main.renderer_auto");   group: "renderer"; checked: App.rendererAuto;   onTriggered: App.setRenderer("auto") }
                MenuItem { text: qsTrId("autodoc.main.renderer_opengl"); group: "renderer"; checked: App.rendererOpenGL; onTriggered: App.setRenderer("opengl") }
            }
            // the terminal and agent panels (terminal.go): Ctrl+h/j/k/l kept from their programs
            Menu {
                title: qsTrId("autodoc.main.terminal_options")
                MenuItem { text: qsTrId("autodoc.main.terminal_panel_keys"); checkable: true; checked: App.terminalPanelKeys; onTriggered: App.toggleTerminalPanelKeys() }
            }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.editor_preferences"); onTriggered: App.openPrefs() }
            // the window's fonts and zoom (fonts.go): a terminal draws in its own, and hides these
            MenuItem { text: qsTrId("autodoc.main.fonts_and_zoom"); visible: App.gui; onTriggered: App.openFonts() }
            // golib draws no separator with nothing after it: in a terminal, where these hide, none
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.zoom_in_ctrl"); visible: App.gui; onTriggered: App.zoomWindow("in") }
            MenuItem { text: qsTrId("autodoc.main.zoom_out_ctrl"); visible: App.gui; onTriggered: App.zoomWindow("out") }
            MenuItem { text: qsTrId("autodoc.main.actual_size_ctrl_0"); visible: App.gui; onTriggered: App.setZoom(100) }
        }
        Menu {
            title: qsTrId("autodoc.main.plugins")
            Instantiator {
                model: App.plugins
                DelegateChooser {
                    role: "kind"
                    DelegateChoice { roleValue: "item"; MenuItem { text: model.label; enabled: model.enabled; onTriggered: App.pluginEntry(model.target) } }
                    DelegateChoice { roleValue: "submenu"; Menu { title: model.label
                        Instantiator { model: model.rows; MenuItem { text: model.label; enabled: model.enabled; onTriggered: App.pluginEntry(model.target) } } } }
                }
            }
            // scripts' rows (menurows.go): a row, or a submenu of a script's rows (autodoc.menu's menu)
            Instantiator {
                model: App.luaMenus
                DelegateChooser {
                    role: "kind"
                    DelegateChoice { roleValue: "item"; MenuItem { text: model.label; enabled: model.enabled; onTriggered: App.luaMenu(model.target) } }
                    DelegateChoice { roleValue: "submenu"; Menu { title: model.label
                        Instantiator { model: model.rows; MenuItem { text: model.label; enabled: model.enabled; onTriggered: App.luaMenu(model.target) } } } }
                }
            }
            // every plugin's commands, by plugin: the SPC p card, which says so when there are none
            MenuItem { text: qsTrId("autodoc.main.commands_spc_p"); onTriggered: App.openPluginChooser() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.browse_the_registry"); onTriggered: App.browseRegistry() }
            MenuItem { text: qsTrId("autodoc.main.add_from_a_git_url"); onTriggered: App.startAddPlugin() }
            MenuItem { text: qsTrId("autodoc.main.manage_plugins"); onTriggered: App.managePlugins() }
        }
        Menu {
            title: qsTrId("autodoc.main.system")
            MenuItem { text: qsTrId("autodoc.main.search_models"); onTriggered: App.openAIModels() }
            MenuItem { text: qsTrId("autodoc.main.agent_profiles"); onTriggered: App.openAgentProfiles() }
            MenuItem { text: qsTrId("autodoc.main.set_up_an_agent"); onTriggered: App.openAgentSetup() }
            MenuSeparator {}
            MenuItem { text: qsTrId("autodoc.main.restart_backend"); onTriggered: App.startRestart() }
        }
        Menu {
            title: qsTrId("autodoc.main.help")
            MenuItem { text: qsTrId("autodoc.main.tutorial"); onTriggered: App.openTutorial() }
            MenuItem { text: qsTrId("autodoc.main.keys"); onTriggered: help.open() }
            MenuItem { text: qsTrId("autodoc.main.about"); onTriggered: about.open() }
        }
        // the dock, the bar's corner: one icon per open widget, or one running with its drawer
        // closed, the editor first; a click shows or hides it (tui/dock.go)
        Flex {
            direction: Tui.Horizontal
            Repeater {
                model: App.dock
                // a hidden panel's icon is dimmed, and lit in the bar's accent twice as it hides; a
                // click shows or hides it (tui/dock.go). color, golib's: a Button wears no palette role
                Button { text: model.icon; flat: true; toolTipText: model.title; onClicked: App.goDock(model.key)
                         color: model.flash ? Theme.menu.accent : model.hidden ? Theme.document.lineNumber : Theme.menu.windowText }
            }
        }
    }

    // ---- the base: a tile manager (golib's), its windows the page and the tileable panels. The
    // page is a window of its own, the fill member: tiled, it reads as the page always has. A panel
    // is tiled while its Layout says so and it fits, else at its own place, as a drawer over the
    // page; Layout › Tiled / Float is each one's (a panel's own keys are live while the keyboard is
    // in it) ----
    TileManager {
        id: tiles
        onTileChanged: App.tileChanged(key, operation, edge, size, tiled)
        // the page: the file, centred, as wide as the ruler; a window of its own that never closes
        FloatWindow {
            id: editorWindow
            key: "editor"
            movable: true; resizable: true; minimizable: true
            maximumWidth: App.pageWidth
            TileManager.edge: Tui.Center
            TileManager.tiled: App.editorTiled
            TileManager.minimumWidth: 40; TileManager.minimumHeight: 8 // the page's least: narrower or shorter, the panel tiled last floats
            TileManager.maximumWidth: App.pageWidth // its most, tiled: the strips beside it take the rest, and alone it is centred
            Frame {
                title: App.fileTitle
                palette.window: Theme.document.window
                palette.windowText: Theme.document.windowText
                palette.highlight: Theme.document.highlight
                palette.highlightedText: Theme.document.highlightedText
                palette.base: Theme.document.base
                palette.text: Theme.document.text
                Flex {
                direction: Tui.Vertical
                // the open file's frontmatter, checked against its workspace's schema as it is typed:
                // shown only while something is wrong, and never in the way of a save
                Text { visible: App.diagnosticsShown; text: App.diagnosticsLine; color: Theme.syntax.alert }
                // the page's keys, live while the keyboard is in the Editor (tui/keys.go's page scope)
                FocusScope {
                    Layout.fillHeight: true
                    keyScope: "autodoc.page" // its keys' commands (tui/keys.go)
                    // ---- the page's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
                    Shortcut { sequence: keyId("autodoc.page.openFind"); onActivated: App.openFind() }
                    Shortcut { sequence: keyId("autodoc.page.findNext"); onActivated: App.findNext() }
                    Shortcut { sequence: keyId("autodoc.page.findPrevious"); onActivated: App.findPrevious() }
                    // ---- end of the page's keys ----
                    Editor {
                        id: editor
                        autoIndent: true
                        contextMenu: true   // right-click: Undo, Redo, Copy, Cut, Paste
                        focus: true
                        palette.highlight: Theme.document.selection
                        palette.highlightedText: Theme.document.selectedText
                        keyset: App.keyset
                        ruler: App.rulerColumn
                        wrap: App.editorWrap
                        lineNumbers: App.lineNumbers
                        // a line's leading spaces as a dimmed ·, its tabs as →: off, the cursor's line, or all
                        indentMarks: App.indentMarks
                        // the theme's accent for the cursor, and its dim tone for the line numbers
                        cursorColor: Theme.document.cursor
                        lineNumberColor: Theme.document.lineNumber
                        onModeChanged: App.syncMode()
                        onTextChanged: App.edited()
                        onCursorPositionChanged: App.cursorMoved()
                        // the command line's (:) w, wq and x, and q and q!: AutoDoc's own save and quit
                        onWriteRequested: App.writeFromEditor(quit)
                        onQuitRequested: App.quitFromEditor(force)
                        SyntaxHighlighter { definition: App.syntaxDefinition }
                        // the Rendered view's Markdown (Ctrl+T switches): the GUI's Editor draws it, the
                        // open note's embeds drawn in place (noterenderer.go); the terminal's Editor has no
                        // Rendered view and ignores it
                        NoteRenderer { mermaid: true }  // mermaid fences as diagrams: flowcharts natively
                        // the Rendered view of an HTML file opened raw: its page, drawn natively (htmldoc.go
                        // turns it on for that file and off for any other); a terminal ignores it
                        HTMLDocumentView {}
                    }
                }
                }
            }
        }

        // the explorer: the workspaces' files and the page's links, a tab each (explorer.go, relations.go),
        // the heading the tab shown's; _, + and x at the strip's right end hide it to the dock, as it is,
        // maximize it (again to restore: the Drawer's own toggleMaximize), or close it. SPC e and SPC l open it on their tab, or go to it, or close it on their own.
        Panel {
            id: explorer
            TileManager.edge: App.explorerTileEdge; TileManager.edges: "left right top bottom"
            TileManager.size: App.explorerTileSize
            // its least: 25 columns hold its strip whole (the tabs' arrows, ≡, and _ + x); 4 rows its frame,
            // its strip and one tree row
            TileManager.minimumWidth: 25; TileManager.minimumHeight: 4
            TileManager.tiled: App.explorerTiled
            heading: App.linksTabShown ? App.relationsTitle : qsTrId("autodoc.main.explorer_2")
            closeOnQ: true
            edge: App.explorerEdge
            size: App.explorerSize
            length: App.explorerLength
            floating: App.explorerFloating
            floatX: App.explorerX; floatY: App.explorerY; floatWidth: App.explorerW; floatHeight: App.explorerH
            onPlaced: App.panelPlaced("explorer", x, y, width, height)
            minimumSize: 15
            onResized: App.panelResized("explorer", size, length)
            onOpened: App.panelOpened("explorer")
            onClosed: App.panelClosed("explorer")
            maximizable: true
            onChanged: App.panelChanged("explorer", operation, maximized)
            TabView {
                id: sideTabs
                windowButtons: true
                currentIndex: App.explorerTab
                onCurrentIndexChanged: App.explorerTabShown(index)
                onMinimizeRequested: App.hideExplorer()
                onMaximizeRequested: explorer.toggleMaximize()
                onCloseRequested: App.closeExplorer()
                Tab {
                    title: "Explorer"
                    // its keys' commands (tui/keys.go): the scope of its FocusScope
                    FocusScope {
                        keyScope: "autodoc.explorer"
                        // ---- the explorer's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
                        Shortcut { sequence: keyId("autodoc.explorer.openFind"); onActivated: App.openFind() }
                        Shortcut { sequence: keyId("autodoc.explorer.findNext"); onActivated: App.findNext() }
                        Shortcut { sequence: keyId("autodoc.explorer.findPrevious"); onActivated: App.findPrevious() }
                        // ---- end of the explorer's keys ----
                        TreeView {
                            id: explorerTree
                            palette.highlight: Theme.document.highlight
                            palette.highlightedText: Theme.document.highlightedText
                            model: App.explorer
                            textRole: "label"
                            onCurrentIndexChanged: App.explorerMoved(index)
                            onActivated: App.explorerActivated(index)
                        }
                    }
                }
                Tab {
                    title: "Links"
                    FocusScope {
                        keyScope: "autodoc.links"
                        // ---- the links's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
                        Shortcut { sequence: keyId("autodoc.links.openFind"); onActivated: App.openFind() }
                        Shortcut { sequence: keyId("autodoc.links.findNext"); onActivated: App.findNext() }
                        Shortcut { sequence: keyId("autodoc.links.findPrevious"); onActivated: App.findPrevious() }
                        Shortcut { sequence: keyId("autodoc.links.relationsDepth"); onActivated: App.relationsDepth() }
                        // ---- end of the links's keys ----
                        ListView {
                            id: relationsList
                            palette.highlight: Theme.document.highlight
                            palette.highlightedText: Theme.document.highlightedText
                            model: App.relations
                            textRole: "label"
                            cursorShape: Tui.PointingHandCursor // a row opens its note
                            currentIndex: App.relationsIndex
                            onCurrentIndexChanged: App.relationsMoved(index)
                            onActivated: App.openRelation(index)
                        }
                    }
                }
            }
        }

        // the terminal: a shell in the workspace's folder, from the edge (or the centre) its preference
        // names, at that place's size; it keeps running while hidden (terminal.go)
        Panel {
            id: terminal
            TileManager.edge: App.terminalTileEdge; TileManager.edges: "left right top bottom"
            TileManager.size: App.terminalTileSize
            // its least: 27 columns hold its strip whole (the tab, +, ≡, and _ + x); 6 rows its frame, its
            // strip and three of the shell's rows
            TileManager.minimumWidth: 27; TileManager.minimumHeight: 6
            TileManager.tiled: App.terminalTiled
            heading: qsTrId("autodoc.main.terminal_2")
            surface: Theme.app.window   // the Window's tone, as a terminal's frame has always had
            edge: App.terminalEdge
            size: App.terminalSize
            length: App.terminalLength
            floating: App.terminalFloating
            floatX: App.terminalX; floatY: App.terminalY; floatWidth: App.terminalW; floatHeight: App.terminalH
            onPlaced: App.panelPlaced("terminal", x, y, width, height)
            // Maximize on its tab menu: the Drawer's own (toggleMaximize), the menu told by changed
            maximizable: true
            onChanged: App.panelChanged("terminal", operation, maximized)
            onResized: App.panelResized("terminal", size, length)
            onOpened: App.panelOpened("terminal")
            onClosed: App.panelClosed("terminal")
            // a tab a shell (terminal.go): + adds one, a tab's menu goes to another, adds or closes one
            TerminalTabs {
                id: terminalView
                // _, + and x at the strip's right end: hide the panel, every process kept; maximize it, as
                // Maximize does (again to restore); stop them all and close it
                windowButtons: true
                onMinimizeRequested: App.toggleTerminal()
                onMaximizeRequested: terminal.toggleMaximize()
                onCloseRequested: App.stopAllShells()
                onChanged: App.widgetsChanged() // a shell started or gone: the dock lists the terminal while one runs
                // maximize's keys (F11) reach the Window with the keyboard in a shell: its program never gets them
                hostKeys: App.terminalHostKeys
                emptyText: "No shell running. Press + to start one."
                // autodoc.terminal.mode (Ctrl+T) toggles its input and Normal mode, in either editor mode; Esc goes to the program
                modeKey: App.terminalModeKey
                vimKeys: false
                // the terminals' theme (terminal.go): its text and background, the strip's the page's
                palette.base: App.terminalBase
                palette.text: App.terminalText
                // the terminals' own face, in a window (fonts.go); a terminal draws in its own font
                cellFont: App.terminalFont
                // a program's colours raised until they read on that background: ls's yellow and a
                // prompt's cyan are written for a dark terminal, and wash out on a light theme
                minimumContrast: 4.5
                // the theme's caret colour; its shape says the mode: a block in normal, a bar typing
                cursorColor: App.terminalCursor
            }
        }

        // the agent: an AI agent's CLI, as its default profile says, floating over the page; it keeps
        // running while hidden (agent.go)
        Panel {
            id: agent
            TileManager.edge: App.agentTileEdge; TileManager.edges: "left right top bottom"
            TileManager.size: App.agentTileSize
            // its least: 27 columns hold its strip whole (the tab, +, ≡, and _ + x); 6 rows its frame, its
            // strip and three of the agent's rows
            TileManager.minimumWidth: 27; TileManager.minimumHeight: 6
            TileManager.tiled: App.agentTiled
            heading: App.agentTitle
            surface: Theme.app.window   // the Window's tone, as the agent's frame has always had
            edge: App.agentEdge
            size: App.agentSize
            length: App.agentLength
            floating: App.agentFloating
            floatX: App.agentX; floatY: App.agentY; floatWidth: App.agentW; floatHeight: App.agentH
            onPlaced: App.panelPlaced("agent", x, y, width, height)
            maximizable: true
            onChanged: App.panelChanged("agent", operation, maximized)
            onOpened: App.panelOpened("agent")
            onClosed: App.panelClosed("agent")
            onResized: App.panelResized("agent", size, length)
            // a tab an agent, named by its codename (agenttabs.go): + starts or goes to a profile, a
            // tab's menu goes to another, restarts or closes it, or opens the profiles
            TerminalTabs {
                id: agentView
                // _, + and x at the strip's right end: hide the panel, every process kept; maximize it, as
                // Maximize does (again to restore); stop them all and close it
                windowButtons: true
                onMinimizeRequested: App.toggleAgent()
                onMaximizeRequested: agent.toggleMaximize()
                onCloseRequested: App.stopAllAgents()
                // maximize's keys (F11) reach the Window with the keyboard in a shell: its program never gets them
                hostKeys: App.terminalHostKeys
                emptyText: "No agent running. Press + to start one of the profiles."
                // autodoc.terminal.mode (Ctrl+T) toggles its input and Normal mode, in either editor mode; Esc goes to the program
                modeKey: App.terminalModeKey
                vimKeys: false
                // as the terminal's: the terminals' theme and face, the contrast raised against it
                palette.base: App.terminalBase
                palette.text: App.terminalText
                cellFont: App.terminalFont
                minimumContrast: 4.5
                cursorColor: App.terminalCursor
                onChanged: App.agentTabsChanged()
            }
        }

        // the results: a table a plugin sends (plugintable.go), golib's cell-cursor table over the rows
        // loaded, which asks the plugin for more as the cursor nears their end; at the bottom unless
        // moved. Closing it lets the table go. It acts on the table in the view: the ORDER BY bar sorts
        // the loaded rows, and the buttons and the right-click menu are the results commands, none of
        // which fetches a row.
        Panel {
            id: results
            TileManager.edge: App.resultsTileEdge; TileManager.edges: "left right top bottom"
            TileManager.size: App.resultsTileSize
            // its least: 20 columns hold its ORDER BY field's ▾ and its first button whole; 7 rows its frame,
            // the ORDER BY row, the table's header and rule, one row of results, and the buttons
            TileManager.minimumWidth: 20; TileManager.minimumHeight: 7
            TileManager.tiled: App.resultsTiled
            heading: App.resultsTitle
            closeOnQ: true
            edge: Tui.Bottom
            size: App.resultsSize
            length: App.resultsLength
            floating: App.resultsFloating
            floatX: App.resultsX; floatY: App.resultsY; floatWidth: App.resultsW; floatHeight: App.resultsH
            onPlaced: App.panelPlaced("results", x, y, width, height)
            minimumSize: 10
            onResized: App.panelResized("results", size, length)
            onOpened: App.panelOpened("results")
            onClosed: App.panelClosed("results")
            Flex {
                direction: Tui.Vertical
                // the loaded rows tidied as SQL writes it: the clause (ORDER BY, WHERE), then its text,
                // which Enter applies, each clause keeping its own; the line is the clause's error, the
                // fields a word typed may be, or what the view holds of the result
                Flex {
                    direction: Tui.Horizontal
                    ComboBox { id: resultsClause; model: App.resultsClauses; textRole: "label"; valueRole: "key"; maxWidth: 14
                        currentIndex: App.resultsClauseIndex; onActivated: App.resultsClauseChosen(index, resultsText.text) }
                    Text { text: " " }
                    TextField {
                        id: resultsText
                        Layout.fillWidth: true
                        text: App.resultsClauseText
                        placeholderText: qsTrId("autodoc.main.over_the_loaded_rows_order_by_column_asc_desc_wh")
                        onAccepted: App.applyResultsClause(text)
                        onTextEdited: App.resultsClauseTyped(text)
                    }
                    Text { text: " " }
                    Text { text: App.resultsSortLine }
                }
                TableView {
                    id: resultsTable
                    Layout.fillHeight: true
                    palette.highlight: Theme.document.highlight
                    palette.highlightedText: Theme.document.highlightedText
                    // the cursor while the keys are elsewhere (the editor, the bar)
                    palette.inactive.highlight: Theme.app.inactive.highlight
                    palette.inactive.highlightedText: Theme.app.inactive.highlightedText
                    model: App.resultsRows
                    cellCursor: true
                    sortable: true
                    rowNumbers: true
                    contextMenu: true
                    orderBy: App.resultsOrder
                    filter: App.resultsFilter
                    onOrderByChanged: App.resultsOrderChanged(orderBy)
                }
                Flex {
                    direction: Tui.Horizontal
                    Text { text: " "; Layout.fillWidth: true }
                    Button { text: qsTrId("autodoc.main.copy"); toolTipText: "the selected cells, or the cursor's, as TSV (y in the table; the right-click menu has CSV, JSON and Markdown)"; onClicked: App.copyResults() }
                    Text { text: " " }
                    Button { text: qsTrId("autodoc.main.export_loaded_rows"); toolTipText: "the rows loaded, as shown, into a CSV or JSON file"; onClicked: App.startExportResults() }
                    Text { text: " " }
                    Button { text: qsTrId("autodoc.main.insert_as_markdown"); toolTipText: "the rows loaded now, as shown, as a Markdown table at the page's cursor"; onClicked: App.insertResults() }
                    Text { text: " " }
                    Button { text: qsTrId("autodoc.main.close_q"); toolTipText: "close the results"; onClicked: App.toggleResults() }
                    Text { text: " "; Layout.fillWidth: true }
                }
            }
        }

        // the todos (todos.go): one store at a time, chosen in the dropdown along the top, its tasks a
        // tab per status, as autofinder's todos panel; at the left edge unless moved, and large: 65% of
        // the window across, 80% along, centred (todosDefault), until the user sizes or places it
        Panel {
            id: todos
            TileManager.edge: App.todosTileEdge; TileManager.edges: "left right top bottom"
            TileManager.size: App.todosTileSize
            // its least: 20 columns hold a tab, the tabs' arrows and ≡, and the store's first buttons whole;
            // 16 rows its frame, the store, the tabs, the table's header and one row of tasks, the details
            // (which take half of what the table leaves) and the status line
            TileManager.minimumWidth: 20; TileManager.minimumHeight: 16
            TileManager.tiled: App.todosTiled
            heading: App.todosTitle
            closeOnQ: true
            edge: App.todosEdge
            size: App.todosSize
            length: App.todosLength
            floating: App.todosFloating
            floatX: App.todosX; floatY: App.todosY; floatWidth: App.todosW; floatHeight: App.todosH
            onPlaced: App.panelPlaced("todos", x, y, width, height)
            // 25%, not the side panels' 15: the top bar's icons, and the action row's widest button,
            // "[ Status… ]", whole inside the frame on an 80-column screen; its four fit from 55% there
            minimumSize: 25
            onResized: App.panelResized("todos", size, length)
            onOpened: App.panelOpened("todos")
            onClosed: App.panelClosed("todos")
            Flex {
                direction: Tui.Vertical
                // the panel's keys, live while the keyboard is in the top bar or the tabs: NOT in the
                // details pane below, whose read-only Editor lets the edit keys it refuses (a d s i o)
                // bubble, and a key read there must never change or remove the task being read
                FocusScope {
                    Layout.fillHeight: true
                    keyScope: "autodoc.todos" // its keys' commands (tui/keys.go)
                    // ---- the todos's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
                    Shortcut { sequence: keyId("autodoc.todos.todoAdd"); onActivated: App.todoAdd() }
                    Shortcut { sequence: keyId("autodoc.todos.todoRemove"); onActivated: App.todoRemove() }
                    Shortcut { sequence: keyId("autodoc.todos.todoChooseStatus"); onActivated: App.todoChooseStatus() }
                    Shortcut { sequence: keyId("autodoc.todos.todoToggleDetail"); onActivated: App.todoToggleDetail() }
                    Shortcut { sequence: keyId("autodoc.todos.todoAssign"); onActivated: App.todoAssign() }
                    Shortcut { sequence: keyId("autodoc.todos.todoTabBefore"); onActivated: App.todoTabBefore() }
                    Shortcut { sequence: keyId("autodoc.todos.todoTabAfter"); onActivated: App.todoTabAfter() }
                    Shortcut { sequence: keyId("autodoc.todos.todoMonthsBack"); onActivated: App.todoMonthsBack() }
                    Shortcut { sequence: keyId("autodoc.todos.todoFocusStore"); onActivated: App.todoFocusStore() }
                    Shortcut { sequence: keyId("autodoc.todos.todoNew"); onActivated: App.todoNew() }
                    Shortcut { sequence: keyId("autodoc.todos.todoLoad"); onActivated: App.todoLoad() }
                    Shortcut { sequence: keyId("autodoc.todos.todoLabel"); onActivated: App.todoLabel() }
                    Shortcut { sequence: keyId("autodoc.todos.todoRefresh"); onActivated: App.todoRefresh() }
                    Shortcut { sequence: keyId("autodoc.todos.todoMove"); onActivated: App.todoMove() }
                    Shortcut { sequence: keyId("autodoc.todos.open.todoKeys"); onActivated: todoKeys.open() }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.0"); onActivated: App.todosTab(0) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.1"); onActivated: App.todosTab(1) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.2"); onActivated: App.todosTab(2) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.3"); onActivated: App.todosTab(3) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.4"); onActivated: App.todosTab(4) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.5"); onActivated: App.todosTab(5) }
                    Shortcut { sequence: keyId("autodoc.todos.todoVarEdit"); onActivated: App.todoVarEdit() }
                    Shortcut { sequence: keyId("autodoc.todos.todoVarMachine"); onActivated: App.todoVarMachine() }
                    // ---- end of the todos's keys ----
                    Flex {
                        direction: Tui.Vertical
                        // the store bar, framed, so a line runs between it and the tabs (golib's Text
                        // cuts a rule with an ellipsis): the store shown, the others in its dropdown, on
                        // a row of its own; under it the store's buttons, words (an icon glyph is a box in
                        // a font that lacks it), on the right. The words and a store's name do not fit
                        // one row of the panel's 52 columns on an 80-column screen, so each has its own.
                        Frame {
                            title: qsTrId("autodoc.main.store")
                            Flex {
                                direction: Tui.Vertical
                                ComboBox {
                                    id: todoStore
                                    toolTipText: "the todo store shown: choose another (S)"
                                    model: App.todoStores
                                    textRole: "label"
                                    currentIndex: App.todoStoreIndex
                                    onActivated: App.todoStoreChosen(index)
                                }
                                Flex {
                                    direction: Tui.Horizontal
                                    Text { text: " "; Layout.fillWidth: true }
                                    Button { text: qsTrId("autodoc.main.new"); toolTipText: "a new todo store, in a project's folder (N)"; onClicked: App.todoNew() }
                                    Text { text: " " }
                                    Button { text: qsTrId("autodoc.main.load"); toolTipText: "load a todo store from a folder (O)"; onClicked: App.todoLoad() }
                                    Text { text: " " }
                                    Button { text: qsTrId("autodoc.main.edit_l"); toolTipText: "rename the store: its label (L)"; enabled: App.todoHasStore; onClicked: App.todoLabel() }
                                    Text { text: " " }
                                    Button { text: qsTrId("autodoc.main.refresh"); toolTipText: "refresh the store (R)"; enabled: App.todoHasStore; onClicked: App.todoRefresh() }
                                    Text { text: " " }
                                    Button { text: "?"; toolTipText: "the panel's keys (?)"; onClicked: todoKeys.open() }
                                }
                            }
                        }
                        // a tab per status, each a numbered table of its tasks; Archived its months, and
                        // the open month's tasks beside them
                        TabView {
                            id: todoTabs
                            Layout.fillHeight: true
                            currentIndex: App.todoTab
                            onCurrentIndexChanged: App.todoTabShown(index)
                            Tab {
                                title: "Open"
                                TableView {
                                    id: todoOpenTable
                                    palette.highlight: Theme.document.highlight
                                    palette.highlightedText: Theme.document.highlightedText
                                    model: App.todoOpenRows
                                    currentIndex: App.todoOpenIndex
                                    onCurrentIndexChanged: App.todoMoved(0, index)
                                    onActivated: App.todoActivated(0, index)
                                    TableViewColumn { role: "n"; title: "#"; width: 4 }
                                    TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                                }
                            }
                            Tab {
                                title: "In progress"
                                TableView {
                                    id: todoInProgressTable
                                    palette.highlight: Theme.document.highlight
                                    palette.highlightedText: Theme.document.highlightedText
                                    model: App.todoInProgressRows
                                    currentIndex: App.todoInProgressIndex
                                    onCurrentIndexChanged: App.todoMoved(1, index)
                                    onActivated: App.todoActivated(1, index)
                                    TableViewColumn { role: "n"; title: "#"; width: 4 }
                                    TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                                }
                            }
                            Tab {
                                title: "Deferred"
                                TableView {
                                    id: todoDeferredTable
                                    palette.highlight: Theme.document.highlight
                                    palette.highlightedText: Theme.document.highlightedText
                                    model: App.todoDeferredRows
                                    currentIndex: App.todoDeferredIndex
                                    onCurrentIndexChanged: App.todoMoved(2, index)
                                    onActivated: App.todoActivated(2, index)
                                    TableViewColumn { role: "n"; title: "#"; width: 4 }
                                    TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                                }
                            }
                            Tab {
                                title: "Completed"
                                TableView {
                                    id: todoCompletedTable
                                    palette.highlight: Theme.document.highlight
                                    palette.highlightedText: Theme.document.highlightedText
                                    model: App.todoCompletedRows
                                    currentIndex: App.todoCompletedIndex
                                    onCurrentIndexChanged: App.todoMoved(3, index)
                                    onActivated: App.todoActivated(3, index)
                                    TableViewColumn { role: "n"; title: "#"; width: 4 }
                                    TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                                }
                            }
                            Tab {
                                title: "Archived"
                                Split {
                                    orientation: Tui.Horizontal
                                    ratio: 0.3
                                    TableView {
                                        id: todoMonths
                                        palette.highlight: Theme.document.highlight
                                        palette.highlightedText: Theme.document.highlightedText
                                        model: App.todoMonths
                                        currentIndex: App.todoMonthIndex
                                        onCurrentIndexChanged: App.todoMonthMoved(index)
                                        onActivated: App.todoMonthChosen(index)
                                        TableViewColumn { role: "month"; title: "MONTH"; width: 8 }
                                        TableViewColumn { role: "count"; title: "TASKS"; width: 0 }
                                    }
                                    TableView {
                                        id: todoArchivedTable
                                        palette.highlight: Theme.document.highlight
                                        palette.highlightedText: Theme.document.highlightedText
                                        model: App.todoArchivedRows
                                        currentIndex: App.todoArchivedIndex
                                        onCurrentIndexChanged: App.todoMoved(4, index)
                                        onActivated: App.todoActivated(4, index)
                                        TableViewColumn { role: "n"; title: "#"; width: 4 }
                                        TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                                    }
                                }
                            }
                            // the store's workspace's variables, the same from every route to it: each value and
                            // where it came from (todos_vars.go)
                            Tab {
                                title: "Variables"
                                TableView {
                                    id: todoVarsTable
                                    palette.highlight: Theme.document.highlight
                                    palette.highlightedText: Theme.document.highlightedText
                                    model: App.todoVarsRows
                                    currentIndex: App.todoVarsIndex
                                    onCurrentIndexChanged: App.todoVarMoved(index)
                                    onActivated: App.todoVarEdit()
                                    TableViewColumn { role: "name"; title: "NAME"; width: 16 }
                                    TableViewColumn { role: "from"; title: "FROM"; width: 9 }
                                    TableViewColumn { role: "value"; title: "VALUE"; width: 0 }
                                }
                            }
                        }
                    }
                }
                // the task under the cursor's details (i, o), Markdown, rendered as the pickers' previews
                // are (the GUI's Editor draws it; a terminal's shows the source): at most half the panel,
                // scrolled, so a long description never pushes the tabs away
                Frame {
                    title: qsTrId("autodoc.main.details")
                    visible: App.todoDetailShown
                    Layout.fillHeight: true
                    Editor {
                        cursorColor: Theme.document.cursor // the theme's cursor: a terminal's own may not show on its colours
                        id: todoDetails
                        readOnly: true
                        text: App.todoDetail
                        wrap: true
                        view: Editor.Rendered
                        SyntaxHighlighter { definition: "Markdown" }
                        MarkdownRenderer {}
                    }
                }
                // what needs the user: a file a tab could not list; hidden while there is none
                Text { text: App.todosStatus; visible: App.todosStatusShown; wrapMode: Tui.WordWrap }
                // the action row: one line, only the actions that apply to the task under the cursor,
                // each its key's (its tooltip says which), the others hidden, never greyed; on a store not claimed none, only a
                // note. Which apply follows the cursor (todos.go: syncActions), never a button's own click.
                // the panel's keys again, live on these buttons too (the details pane above has none)
                FocusScope {
                    keyScope: "autodoc.todos" // its keys' commands (tui/keys.go)
                    // ---- the todos task buttons' keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
                    Shortcut { sequence: keyId("autodoc.todos.todoAdd"); onActivated: App.todoAdd() }
                    Shortcut { sequence: keyId("autodoc.todos.todoRemove"); onActivated: App.todoRemove() }
                    Shortcut { sequence: keyId("autodoc.todos.todoChooseStatus"); onActivated: App.todoChooseStatus() }
                    Shortcut { sequence: keyId("autodoc.todos.todoToggleDetail"); onActivated: App.todoToggleDetail() }
                    Shortcut { sequence: keyId("autodoc.todos.todoAssign"); onActivated: App.todoAssign() }
                    Shortcut { sequence: keyId("autodoc.todos.todoTabBefore"); onActivated: App.todoTabBefore() }
                    Shortcut { sequence: keyId("autodoc.todos.todoTabAfter"); onActivated: App.todoTabAfter() }
                    Shortcut { sequence: keyId("autodoc.todos.todoMonthsBack"); onActivated: App.todoMonthsBack() }
                    Shortcut { sequence: keyId("autodoc.todos.todoFocusStore"); onActivated: App.todoFocusStore() }
                    Shortcut { sequence: keyId("autodoc.todos.todoNew"); onActivated: App.todoNew() }
                    Shortcut { sequence: keyId("autodoc.todos.todoLoad"); onActivated: App.todoLoad() }
                    Shortcut { sequence: keyId("autodoc.todos.todoLabel"); onActivated: App.todoLabel() }
                    Shortcut { sequence: keyId("autodoc.todos.todoRefresh"); onActivated: App.todoRefresh() }
                    Shortcut { sequence: keyId("autodoc.todos.todoMove"); onActivated: App.todoMove() }
                    Shortcut { sequence: keyId("autodoc.todos.open.todoKeys"); onActivated: todoKeys.open() }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.0"); onActivated: App.todosTab(0) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.1"); onActivated: App.todosTab(1) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.2"); onActivated: App.todosTab(2) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.3"); onActivated: App.todosTab(3) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.4"); onActivated: App.todosTab(4) }
                    Shortcut { sequence: keyId("autodoc.todos.todosTab.5"); onActivated: App.todosTab(5) }
                    Shortcut { sequence: keyId("autodoc.todos.todoVarEdit"); onActivated: App.todoVarEdit() }
                    Shortcut { sequence: keyId("autodoc.todos.todoVarMachine"); onActivated: App.todoVarMachine() }
                    // ---- end of the todos task buttons' keys ----
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.main.read_only_this_store_was_released"); visible: App.todoReadOnly }
                        Button { id: todoStatusButton; text: qsTrId("autodoc.main.status"); toolTipText: "move the task to another status (s)"; visible: App.todoActStatus; enabled: App.todoActStatus; onClicked: App.todoChooseStatus() }
                        Text { text: " "; visible: App.todoActAssign }
                        Button { text: qsTrId("autodoc.main.assign"); toolTipText: "assign the task to an agent (A)"; visible: App.todoActAssign; enabled: App.todoActAssign; onClicked: App.todoAssign() }
                        Text { text: " "; visible: App.todoActRemove }
                        Button { text: qsTrId("autodoc.main.remove_d"); toolTipText: "remove the task (d)"; visible: App.todoActRemove; enabled: App.todoActRemove; onClicked: App.todoRemove() }
                        Text { text: " "; visible: App.todoActStatus }
                        Button { id: todoAddButton; text: qsTrId("autodoc.main.add"); toolTipText: "add a task (a)"; visible: App.todoActAdd; enabled: App.todoActAdd; onClicked: App.todoAdd() }
                        Button { text: qsTrId("autodoc.main.set"); toolTipText: "set the value, in the KB's file (e)"; visible: App.todoActVarSet; enabled: App.todoActVarSet; onClicked: App.todoVarEdit() }
                        Text { text: " "; visible: App.todoActVarSet }
                        Button { text: qsTrId("autodoc.main.this_machine"); toolTipText: "set the value for this machine only (m)"; visible: App.todoActVarSet; enabled: App.todoActVarSet; onClicked: App.todoVarMachine() }
                        Text { text: " "; visible: App.todoActVarClear }
                        Button { text: qsTrId("autodoc.main.clear_d"); toolTipText: "clear the value set here (d)"; visible: App.todoActVarClear; enabled: App.todoActVarClear; onClicked: App.todoRemove() }
                        Text { text: " "; visible: App.todoActVarAdd }
                        Button { text: qsTrId("autodoc.main.add"); toolTipText: "add a variable, NAME=value (a)"; visible: App.todoActVarAdd; enabled: App.todoActVarAdd; onClicked: App.todoAdd() }
                    }
                }
            }
        }
    }

    StatusBar {
        Dock.edge: Tui.Bottom
        visible: App.statusShown
        palette.window: Theme.status.window
        palette.windowText: Theme.status.windowText
        left: App.statusLeft
        center: App.statusCenter
        right: App.status
        // semantic search, at the right end after the status: a green dot while it answers, a red
        // one while the search is by words
        Text { text: App.semanticMark; color: App.semanticDot }
        Text { text: App.semanticLabel }
    }


    // ---- the panels over the page, outside the tile manager: each a drawer from the edge its
    // preference names, in the one dock style (golib's tui.panels Panel) ----
    // the native HTML preview (File › Preview HTML under --gui): the note's page beside the editor,
    // live as it is edited and following the cursor (htmlpane.go); a terminal never opens it
    Panel {
        id: htmlPane
        heading: qsTrId("autodoc.main.preview")
        edge: Tui.Right
        size: 50
        length: 100
        floating: App.htmlPaneFloating
        floatX: App.htmlPaneX; floatY: App.htmlPaneY; floatWidth: App.htmlPaneW; floatHeight: App.htmlPaneH
        onPlaced: App.panelPlaced("htmlPane", x, y, width, height)
        minimumSize: 20
        onOpened: App.panelOpened("htmlPane")
        onClosed: App.panelClosed("htmlPane")
        paneKeys: "autodoc.htmlPane" // its keys' commands (tui/keys.go): the scope of its FocusScope
        // ---- the HTML preview's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
        Shortcut { sequence: keyId("autodoc.htmlPane.openPaneInBrowser"); onActivated: App.openPaneInBrowser() }
        Shortcut { sequence: keyId("autodoc.htmlPane.closeHTMLPane"); onActivated: App.closeHTMLPane() }
        // ---- end of the HTML preview's keys ----
        Flex {
            direction: Tui.Vertical
            HTMLView {
                id: htmlView
                Layout.fillHeight: true
                onLinkActivated: App.htmlLink(href)
            }
            // as the terminal preview's dialog has them, centred under a rule: the page in the
            // browser (its links, selection and scripts there), and Close; b and q in the pane
            // press them
            Text { text: "────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────"; color: Theme.document.windowText }
            Flex {
                direction: Tui.Horizontal
                Text { text: " "; Layout.fillWidth: true }
                Button { text: qsTrId("autodoc.main.open_in_browser"); toolTipText: "the note's page in the browser"; onClicked: App.openPaneInBrowser() }
                Text { text: " " }
                Button { text: qsTrId("autodoc.main.close_q"); toolTipText: "close the preview"; onClicked: App.closeHTMLPane() }
                Text { text: " "; Layout.fillWidth: true }
            }
        }
    }

    // the card plugins' panels (pluginpanels.go): one per card plugin found when AutoDoc starts,
    // written in here as the program is built; Escape closes one, and its plugin runs on
    // ---- the relation views' panels (the GUI's) ----
    // ---- the card plugins' panels ----

    // a script's card (autodoc.ui.card, luacard.go): its Markdown drawn Rendered in the GUI, where a
    // vega-lite fence is a chart; a terminal shows the source. One card at a time; :usage is one
    Panel {
        id: luaCard
        heading: App.luaCardTitle
        closeOnQ: true
        edge: Tui.Right
        size: App.luaCardSize
        length: App.luaCardLength
        floating: App.luaCardFloating
        floatX: App.luaCardX; floatY: App.luaCardY; floatWidth: App.luaCardW; floatHeight: App.luaCardH
        onPlaced: App.panelPlaced("luaCard", x, y, width, height)
        minimumSize: 15
        onResized: App.panelResized("luaCard", size, length)
        onOpened: App.panelOpened("luaCard")
        onClosed: App.panelClosed("luaCard")
        Editor {
            cursorColor: Theme.document.cursor // the theme's cursor: a terminal's own may not show on its colours
            id: luaCardView
            readOnly: true
            keepPlace: true
            wrap: true
            view: Editor.Rendered
            text: App.luaCardText
            SyntaxHighlighter { definition: "Markdown" }
            MarkdownRenderer { mermaid: true }
        }
    }

    SearchPicker { id: searchPicker }
    Find { id: findDialog }
    FileOpen { id: fileOpen }
    BufferPick { id: bufferPick }
    GoPick { id: widgetPick }
    OutlinePicker { id: outlinePicker }
    AgentAssign { id: agentAssignPicker }
    AuditAgent { id: auditAgentPicker }
    LuaPick { id: luaPicker }
    TodoStatus { id: todoStatusPicker }
    RegistryBrowse { id: registryPicker }
    TerminalLook { id: terminalLook }
    FileName { id: fileName }
    OpenAnyFile { id: openAnyFile }
    NewAnyFile { id: newAnyFile }
    ExportResults { id: exportResults }
    ExportResultsFile { id: exportResultsFile }
    UnsavedFile { id: unsavedFile }
    Recovery { id: recoveryDialog }
    OpenHTML { id: openHTML }
    FileConflict { id: fileConflict }
    WorkspacePicker { id: workspacePicker }
    WorkspaceManager { id: workspaceManager }
    WorkspaceSettings { id: workspaceSettings }
    WorkspaceRoot { id: workspaceRoot }
    PluginAdd { id: pluginAdd }
    PluginConfirm { id: pluginConfirm }
    PluginManager { id: pluginManager }
    Preferences { id: preferences }
    Fonts { id: fonts }
    Diagram { id: diagram }
    HtmlPreview { id: htmlPreview }
    AIModels { id: aiModels }
    ProviderEdit { id: providerEdit }
    RankerEdit { id: rankerEdit }
    Recent { id: recent }
    AgentProfiles { id: agentProfiles }
    AgentEdit { id: agentEdit }
    VimQuestion { id: vimQuestion }
    AgentSetup { id: agentSetup }
    EmbeddingSetup { id: embeddingSetup }
    AgentSwitch { id: agentSwitch }
    RankerWindow { id: rankerWindow }
    Mismatch { id: mismatch }
    Registrations { id: registrations }
    Vectors { id: vectorsDialog }
    // ---- the cards of keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
    Leader { id: leader }
    AgentsCard { id: agentsCard }
    TodoKeys { id: todoKeys }
    // ---- end of the generated cards ----
    PluginChooser { id: pluginChooser }
    LuaKeys { id: luaKeys }
    Help { id: help }
    Arrange { id: arrange }
    Tutorial { id: tutorial }
    About { id: about }
    TodoNew { id: todoNew }
    TodoLabel { id: todoLabel }
    TodoVar { id: todoVar }
    TodoAssign { id: todoAssign }
    TodoFolder { id: todoFolder; title: qsTrId("autodoc.main.todos_load_a_store_its_project_s_folder") }
    TodoFolder { id: todoFolderNew; title: qsTrId("autodoc.main.todos_a_new_store_the_project_s_folder") }
    TodoFolder { id: todoFolderMove; title: qsTrId("autodoc.main.todos_move_the_store_its_new_folder") }
    Notifications { id: notifications }
    Confirm { id: confirm }
}
