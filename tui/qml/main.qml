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
            title: "&File"
            MenuItem { text: "&New file…"; onTriggered: App.newFile() }
            MenuItem { text: "Open &document…"; onTriggered: App.openPicker() }
            MenuItem { text: "&Open file…"; onTriggered: App.openFileDialog() }
            MenuItem { text: "Recen&t files…"; onTriggered: App.openRecent() }
            MenuSeparator {}
            MenuItem { text: "&Save"; onTriggered: App.save() }
            MenuItem { text: "&Reload from disk"; onTriggered: App.reload() }
            MenuItem { text: "Close &buffer"; onTriggered: App.closeBuffer() }
            MenuSeparator {}
            MenuItem { text: "Preview &HTML"; onTriggered: App.previewHTML() }
            // what draws a window's Mermaid diagrams (preview.go); the terminal's are mermaid.js's
            Menu {
                title: "Preview o&ption"
                visible: App.gui
                MenuItem { text: "&Native Mermaid engine"; group: "mermaidEngine"; checked: App.mermaidNative; onTriggered: App.setMermaidEngine("native") }
                MenuItem { text: "Mermaid.&js";            group: "mermaidEngine"; checked: App.mermaidJS;     onTriggered: App.setMermaidEngine("mermaidjs") }
            }
            MenuItem { text: "Preview &Mermaid diagram…"; onTriggered: App.previewDiagram() }
            MenuItem { text: "Open with Default &App"; onTriggered: App.openWithDefaultApp() }
            MenuSeparator {}
            MenuItem { text: "E&xit"; onTriggered: App.quit() }
        }
        Menu {
            title: "&Go"
            MenuItem { text: "&Search…"; onTriggered: App.openSearch() }
            MenuItem { text: "&Outline…"; onTriggered: App.openOutline() }
            MenuItem { text: "&Buffers…"; onTriggered: App.openBuffers() }
            MenuItem { text: "W&idget…"; onTriggered: App.openWidgets() }
            MenuSeparator {}
            MenuItem { text: "&Explorer"; onTriggered: App.toggleExplorer() }
            MenuItem { text: "&Notifications…"; onTriggered: App.openNotices() }
            MenuSeparator {}
            // the panels that stay open, checked while they show
            MenuItem { text: "A&gent"; checkable: true; checked: App.agentShown; onTriggered: App.toggleAgent() }
            MenuItem { text: "&Terminal"; checkable: true; checked: App.terminalShown; onTriggered: App.toggleTerminal() }
            MenuItem { text: "Res&ults"; checkable: true; checked: App.resultsShown; onTriggered: App.toggleResults() }
            MenuSeparator {}
            MenuItem { text: "A&rrange panel…"; onTriggered: App.arrangePanel() }
            MenuItem { text: "Reset panel la&yout"; onTriggered: App.resetPanelLayout() }
            MenuSeparator {}
            MenuItem { text: "&Workspace…"; onTriggered: App.pickWorkspace() }
            MenuItem { text: "&Manage workspaces…"; onTriggered: App.manageWorkspaces() }
        }
        // the views of the shown document's relations (relations.go)
        Menu {
            title: "Re&lations"
            MenuItem { text: "&Links  (SPC l)"; onTriggered: App.toggleRelations() }
            // ---- the relation views' menu items (the GUI's) ----
        }
        // Search: the page's find for the Text mode too, where / types a slash, and the workspace's
        Menu {
            title: "Sea&rch"
            MenuItem { text: "&Find in page…"; onTriggered: App.openFindInPage() }
            MenuItem { text: "Find &next"; onTriggered: App.findAgainNext() }
            MenuItem { text: "Find &previous"; onTriggered: App.findAgainPrevious() }
            MenuItem { text: "&Clear find"; onTriggered: App.clearFind() }
            MenuSeparator {}
            MenuItem { text: "&Search files…"; onTriggered: App.openSearch() }
            MenuSeparator {}
            MenuItem { text: "C&ommand line…  (Ctrl+;)"; onTriggered: App.openCommandLine() }
        }
        Menu {
            title: "&View"
            MenuItem { text: "&Status line"; checkable: true; checked: App.statusShown; onTriggered: App.toggleStatusLine() }
            MenuItem { text: "&Hide the menu bar"; checkable: true; checked: App.menuAutoHide; onTriggered: App.toggleMenuBar() }
            MenuSeparator {}
            MenuItem { text: "&Wrap long lines"; checkable: true; checked: App.editorWrap; onTriggered: App.toggleWrap() }
            MenuItem { text: "Line &numbers"; checkable: true; checked: App.lineNumbers; onTriggered: App.toggleLineNumbers() }
            Menu {
                title: "Indentation &marks"
                MenuItem { text: "&1. Off";                  group: "indentMarks"; checked: App.indentMarksOff;        onTriggered: App.setIndentMarks("off") }
                MenuItem { text: "&2. On the cursor's line"; group: "indentMarks"; checked: App.indentMarksCursorLine; onTriggered: App.setIndentMarks("cursorLine") }
                MenuItem { text: "&3. On every line";        group: "indentMarks"; checked: App.indentMarksAll;        onTriggered: App.setIndentMarks("all") }
            }
            MenuSeparator {}
            MenuItem { text: "&Image previews"; checkable: true; checked: App.imagePreviews; onTriggered: App.toggleImagePreviews() }
        }
        // the todo stores, AutoDoc's own view of them (todos.go)
        Menu {
            title: "&Todos"
            MenuItem { text: "&Open todos…  (SPC x)"; onTriggered: App.openTodos() }
            MenuSeparator {}
            MenuItem { text: "&New store…"; onTriggered: App.todoNew() }
            MenuItem { text: "&Load store…"; onTriggered: App.todoLoad() }
        }
        Menu {
            title: "&Options"
            Menu {
                title: "Editor &mode"
                // a shared group makes these a radio set, as editor-qml's are
                MenuItem { text: "&1. Vim  (modal)";     group: "keymap"; checked: App.keymapVim;  onTriggered: App.setKeymap("vim") }
                MenuItem { text: "&2. Text (modeless)";  group: "keymap"; checked: App.keymapText; onTriggered: App.setKeymap("text") }
            }
            Menu {
                title: "&Theme"
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
            MenuSeparator {}
            MenuItem { text: "&Editor preferences…"; onTriggered: App.openPrefs() }
            // the window's fonts and zoom (fonts.go): a terminal draws in its own, and hides these
            MenuItem { text: "&Fonts and zoom…"; visible: App.gui; onTriggered: App.openFonts() }
            // golib draws no separator with nothing after it: in a terminal, where these hide, none
            MenuSeparator {}
            MenuItem { text: "Zoom &in  (Ctrl+=)"; visible: App.gui; onTriggered: App.zoomWindow("in") }
            MenuItem { text: "Zoom &out  (Ctrl+-)"; visible: App.gui; onTriggered: App.zoomWindow("out") }
            MenuItem { text: "&Actual size  (Ctrl+0)"; visible: App.gui; onTriggered: App.setZoom(100) }
        }
        Menu {
            title: "&Plugins"
            Instantiator {
                model: App.plugins
                DelegateChooser {
                    role: "kind"
                    DelegateChoice { roleValue: "item"; MenuItem { text: model.label; enabled: model.enabled; onTriggered: App.pluginEntry(model.target) } }
                    DelegateChoice { roleValue: "submenu"; Menu { title: model.label
                        Instantiator { model: model.rows; MenuItem { text: model.label; enabled: model.enabled; onTriggered: App.pluginEntry(model.target) } } } }
                }
            }
            Instantiator { model: App.luaMenus; MenuItem { text: model.label; onTriggered: App.luaMenu(model.target) } }
            // every plugin's commands, by plugin: the SPC p card, which says so when there are none
            MenuItem { text: "&Commands…  (SPC p)"; onTriggered: App.openPluginChooser() }
            MenuSeparator {}
            MenuItem { text: "&Browse the registry…"; onTriggered: App.browseRegistry() }
            MenuItem { text: "&Add from a git URL…"; onTriggered: App.startAddPlugin() }
            MenuItem { text: "&Manage plugins…"; onTriggered: App.managePlugins() }
        }
        Menu {
            title: "&System"
            MenuItem { text: "&Search models…"; onTriggered: App.openAIModels() }
            MenuItem { text: "A&gent profiles…"; onTriggered: App.openAgentProfiles() }
            MenuItem { text: "Set up an &agent…"; onTriggered: App.openAgentSetup() }
            MenuSeparator {}
            MenuItem { text: "&Restart backend…"; onTriggered: App.startRestart() }
        }
        Menu {
            title: "&Help"
            MenuItem { text: "&Tutorial…"; onTriggered: App.openTutorial() }
            MenuItem { text: "&Keys"; onTriggered: help.open() }
            MenuItem { text: "&About"; onTriggered: about.open() }
        }
        // the dock, the bar's corner: one icon per open widget, or one running with its drawer
        // closed, the editor first; a click shows or hides it (tui/dock.go)
        Flex {
            direction: Tui.Horizontal
            Repeater {
                model: App.dock
                // a hidden panel's icon is dimmed; a click shows or hides it (tui/dock.go)
                Button { text: model.icon; flat: true; toolTipText: model.title; onClicked: App.goDock(model.key)
                         palette.buttonText: model.hidden ? Theme.document.lineNumber : Theme.menu.windowText }
            }
        }
    }

    // ---- the page: the file, centred, as wide as the ruler ----
    Frame {
        title: App.fileTitle
        maximumWidth: App.pageWidth
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

    // ---- the panels: drawers over the page, each from the edge its preference names, in the one
    // dock style (views/Panel.qml); a panel's own keys are live while the keyboard is in it ----
    Panel {
        id: explorer
        heading: "explorer"
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
        paneKeys: "autodoc.explorer" // its keys' commands (tui/keys.go): the scope of its FocusScope
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
    // the native HTML preview (File › Preview HTML under --gui): the note's page beside the editor,
    // live as it is edited and following the cursor (htmlpane.go); a terminal never opens it
    Panel {
        id: htmlPane
        heading: "preview"
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
                Button { text: "Open in &browser"; toolTipText: "the note's page in the browser"; onClicked: App.openPaneInBrowser() }
                Text { text: " " }
                Button { text: "Close (&q)"; toolTipText: "close the preview"; onClicked: App.closeHTMLPane() }
                Text { text: " "; Layout.fillWidth: true }
            }
        }
    }
    Panel {
        id: links
        heading: App.relationsTitle
        closeOnQ: true
        edge: App.relationsEdge
        size: App.linksSize
        length: App.linksLength
        floating: App.linksFloating
        floatX: App.linksX; floatY: App.linksY; floatWidth: App.linksW; floatHeight: App.linksH
        onPlaced: App.panelPlaced("links", x, y, width, height)
        minimumSize: 15
        onResized: App.panelResized("links", size, length)
        onOpened: App.panelOpened("links")
        onClosed: App.panelClosed("links")
        paneKeys: "autodoc.links" // its keys' commands (tui/keys.go): the scope of its FocusScope
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
            currentIndex: App.relationsIndex
            onCurrentIndexChanged: App.relationsMoved(index)
            onActivated: App.openRelation(index)
        }
    }
    // the terminal: a shell in the workspace's folder, from the edge (or the centre) its preference
    // names, at that place's size; it keeps running while hidden (terminal.go)
    Panel {
        id: terminal
        heading: "terminal"
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
                    placeholderText: "over the loaded rows: ORDER BY column [ASC|DESC], …; WHERE column > 1 AND …"
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
                Button { text: "Copy"; toolTipText: "the selected cells, or the cursor's, as TSV (y in the table; the right-click menu has CSV, JSON and Markdown)"; onClicked: App.copyResults() }
                Text { text: " " }
                Button { text: "Export loaded rows…"; toolTipText: "the rows loaded, as shown, into a CSV or JSON file"; onClicked: App.startExportResults() }
                Text { text: " " }
                Button { text: "Insert as Markdown"; toolTipText: "the rows loaded now, as shown, as a Markdown table at the page's cursor"; onClicked: App.insertResults() }
                Text { text: " " }
                Button { text: "Close (&q)"; toolTipText: "close the results"; onClicked: App.toggleResults() }
                Text { text: " "; Layout.fillWidth: true }
            }
        }
    }

    // the card plugins' panels (pluginpanels.go): one per card plugin found when AutoDoc starts,
    // written in here as the program is built; Escape closes one, and its plugin runs on
    // ---- the relation views' panels (the GUI's) ----
    // ---- the card plugins' panels ----

    // the todos (todos.go): one store at a time, chosen in the dropdown along the top, its tasks a
    // tab per status, as autofinder's todos panel; at the left edge unless moved, and large: 65% of
    // the window across, 80% along, centred (todosDefault), until the user sizes or places it
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
    Panel {
        id: todos
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
                        title: "store"
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
                                Button { text: "&New"; toolTipText: "a new todo store, in a project's folder (N)"; onClicked: App.todoNew() }
                                Text { text: " " }
                                Button { text: "L&oad"; toolTipText: "load a todo store from a folder (O)"; onClicked: App.todoLoad() }
                                Text { text: " " }
                                Button { text: "Edit (&L)"; toolTipText: "rename the store: its label (L)"; enabled: App.todoHasStore; onClicked: App.todoLabel() }
                                Text { text: " " }
                                Button { text: "&Refresh"; toolTipText: "refresh the store (R)"; enabled: App.todoHasStore; onClicked: App.todoRefresh() }
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
                title: "details"
                visible: App.todoDetailShown
                Layout.fillHeight: true
                Editor {
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
                    Text { text: "read-only until the cutover"; visible: App.todoReadOnly }
                    Button { id: todoStatusButton; text: "&Status…"; toolTipText: "move the task to another status (s)"; visible: App.todoActStatus; enabled: App.todoActStatus; onClicked: App.todoChooseStatus() }
                    Text { text: " "; visible: App.todoActAssign }
                    Button { text: "&Assign"; toolTipText: "assign the task to an agent (A)"; visible: App.todoActAssign; enabled: App.todoActAssign; onClicked: App.todoAssign() }
                    Text { text: " "; visible: App.todoActRemove }
                    Button { text: "Remove (&d)"; toolTipText: "remove the task (d)"; visible: App.todoActRemove; enabled: App.todoActRemove; onClicked: App.todoRemove() }
                    Text { text: " "; visible: App.todoActStatus }
                    Button { id: todoAddButton; text: "&Add"; toolTipText: "add a task (a)"; visible: App.todoActAdd; enabled: App.todoActAdd; onClicked: App.todoAdd() }
                    Button { text: "S&et"; toolTipText: "set the value, in the KB's file (e)"; visible: App.todoActVarSet; enabled: App.todoActVarSet; onClicked: App.todoVarEdit() }
                    Text { text: " "; visible: App.todoActVarSet }
                    Button { text: "This &machine"; toolTipText: "set the value for this machine only (m)"; visible: App.todoActVarSet; enabled: App.todoActVarSet; onClicked: App.todoVarMachine() }
                    Text { text: " "; visible: App.todoActVarClear }
                    Button { text: "Clear (&d)"; toolTipText: "clear the value set here (d)"; visible: App.todoActVarClear; enabled: App.todoActVarClear; onClicked: App.todoRemove() }
                    Text { text: " "; visible: App.todoActVarAdd }
                    Button { text: "&Add"; toolTipText: "add a variable, NAME=value (a)"; visible: App.todoActVarAdd; enabled: App.todoActVarAdd; onClicked: App.todoAdd() }
                }
            }
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
    AgentSetup { id: agentSetup }
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
    TodoFolder { id: todoFolder; title: "todos: load a store: its project's folder" }
    TodoFolder { id: todoFolderNew; title: "todos: a new store: the project's folder" }
    TodoFolder { id: todoFolderMove; title: "todos: move the store: its new folder" }
    Notifications { id: notifications }
    Confirm { id: confirm }
}
