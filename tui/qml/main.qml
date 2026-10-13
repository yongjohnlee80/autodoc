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
    syntax.constant: Theme.syntax.constant
    syntax.comment: Theme.syntax.comment
    syntax.alert: Theme.syntax.alert
    syntax.import: Theme.syntax.import
    syntax.operator: Theme.syntax.operator

    // the Window's keys fire whichever widget has focus, when it leaves them (tui/keys.go)
    // ---- the Window's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
    Shortcut { sequence: "Space"; onActivated: leader.open() }
    Shortcut { sequence: "Ctrl+Space"; onActivated: leader.open() }
    Shortcut { sequence: "Ctrl+H"; onActivated: App.movePane("h") }
    Shortcut { sequence: "Ctrl+J"; onActivated: App.movePane("j") }
    Shortcut { sequence: "Ctrl+K"; onActivated: App.movePane("k") }
    Shortcut { sequence: "Ctrl+L"; onActivated: App.movePane("l") }
    Shortcut { sequence: "Ctrl+S"; onActivated: App.save() }
    Shortcut { sequence: "Ctrl+N"; onActivated: App.newFile() }
    Shortcut { sequence: "Ctrl+G"; onActivated: App.openSearch() }
    Shortcut { sequence: "Ctrl+;"; onActivated: App.openCommandLine() }
    Shortcut { sequence: "/"; onActivated: App.openFind() }
    Shortcut { sequence: "n"; onActivated: App.findNext() }
    Shortcut { sequence: "Shift+N"; onActivated: App.findPrevious() }
    Shortcut { sequence: "Ctrl+W"; onActivated: App.pickWorkspace() }
    Shortcut { sequence: "Ctrl+Q"; onActivated: App.quit() }
    Shortcut { sequence: "F11"; onActivated: App.maximizePanel() }
    Shortcut { sequence: "F1"; onActivated: help.open() }
    Shortcut { sequence: "Ctrl+="; onActivated: App.zoomWindow("in") }
    Shortcut { sequence: "Ctrl+-"; onActivated: App.zoomWindow("out") }
    Shortcut { sequence: "Ctrl+0"; onActivated: App.setZoom(100) }
    Shortcut { sequence: "?"; onActivated: App.toggleVimKeys() }
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
            MenuSeparator {}
            MenuItem { text: "Preview &HTML"; onTriggered: App.previewHTML() }
            MenuItem { text: "Preview &Mermaid diagram…"; onTriggered: App.previewDiagram() }
            MenuItem { text: "Open with Default &App"; onTriggered: App.openWithDefaultApp() }
            MenuSeparator {}
            MenuItem { text: "E&xit"; onTriggered: App.quit() }
        }
        Menu {
            title: "&Go"
            MenuItem { text: "&Search…"; onTriggered: App.openSearch() }
            MenuItem { text: "&Outline…"; onTriggered: App.openOutline() }
            MenuSeparator {}
            MenuItem { text: "&Explorer"; onTriggered: App.toggleExplorer() }
            MenuItem { text: "Re&lations"; onTriggered: App.toggleRelations() }
            MenuItem { text: "&Notifications…"; onTriggered: App.openNotices() }
            MenuSeparator {}
            // the panels that stay open, checked while they show
            MenuItem { text: "A&gent"; checkable: true; checked: App.agentShown; onTriggered: App.toggleAgent() }
            MenuItem { text: "&Terminal"; checkable: true; checked: App.terminalShown; onTriggered: App.toggleTerminal() }
            MenuSeparator {}
            MenuItem { text: "A&rrange panel…"; onTriggered: App.arrangePanel() }
            MenuItem { text: "Reset panel la&yout"; onTriggered: App.resetPanelLayout() }
            MenuSeparator {}
            MenuItem { text: "&Workspace…"; onTriggered: App.pickWorkspace() }
            MenuItem { text: "&Manage workspaces…"; onTriggered: App.manageWorkspaces() }
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
            MenuItem { text: "La&bel store…  (L)"; onTriggered: App.todoLabel() }
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
                MenuItem { text: "&Dark";  group: "theme"; checked: App.themeDark;  onTriggered: App.useTheme("dark") }
                MenuItem { text: "&Light"; group: "theme"; checked: App.themeLight; onTriggered: App.useTheme("light") }
                MenuItem { text: "&Mono";  group: "theme"; checked: App.themeMono;  onTriggered: App.useTheme("mono") }
                MenuItem { text: "&Retro"; group: "theme"; checked: App.themeRetro; onTriggered: App.useTheme("retro") }
                MenuItem { text: "&Sepia"; group: "theme"; checked: App.themeSepia; onTriggered: App.useTheme("sepia") }
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
            MenuSeparator {}
            MenuItem { text: "&Add from a git URL…"; onTriggered: App.startAddPlugin() }
            MenuItem { text: "&Manage plugins…"; onTriggered: App.managePlugins() }
        }
        Menu {
            title: "&System"
            MenuItem { text: "&Search models…"; onTriggered: App.openAIModels() }
            MenuItem { text: "A&gent profiles…"; onTriggered: App.openAgentProfiles() }
            MenuSeparator {}
            MenuItem { text: "&Restart backend…"; onTriggered: App.startRestart() }
        }
        Menu {
            title: "&Help"
            align: Tui.Right
            MenuItem { text: "&Tutorial…"; onTriggered: App.openTutorial() }
            MenuItem { text: "&Keys"; onTriggered: help.open() }
            MenuItem { text: "&About"; onTriggered: about.open() }
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
            // ---- the page's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
            Shortcut { sequence: "/"; onActivated: App.openFind() }
            Shortcut { sequence: "n"; onActivated: App.findNext() }
            Shortcut { sequence: "Shift+N"; onActivated: App.findPrevious() }
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
                // the Rendered view's Markdown (Ctrl+T switches): the GUI's Editor draws it; the
                // terminal's Editor has no Rendered view and ignores it
                MarkdownRenderer { mermaid: true }  // mermaid fences as diagrams: flowcharts natively
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
        // ---- the explorer's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
        Shortcut { sequence: "/"; onActivated: App.openFind() }
        Shortcut { sequence: "n"; onActivated: App.findNext() }
        Shortcut { sequence: "Shift+N"; onActivated: App.findPrevious() }
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
        HTMLView {
            id: htmlView
            onLinkActivated: App.htmlLink(href)
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
        // ---- the links's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
        Shortcut { sequence: "/"; onActivated: App.openFind() }
        Shortcut { sequence: "n"; onActivated: App.findNext() }
        Shortcut { sequence: "Shift+N"; onActivated: App.findPrevious() }
        Shortcut { sequence: "d"; onActivated: App.relationsDepth() }
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
            emptyText: "No shell running. Press + to start one."
            vimKeys: App.keymapVim
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
            emptyText: "No agent running. Press + to start one of the profiles."
            vimKeys: App.keymapVim
            // as the terminal's: the terminals' theme and face, the contrast raised against it
            palette.base: App.terminalBase
            palette.text: App.terminalText
            cellFont: App.terminalFont
            minimumContrast: 4.5
            cursorColor: App.terminalCursor
            onChanged: App.agentTabsChanged()
        }
    }

    // the todos (todos.go): the stores open, each its buckets and tasks, as autofinder's todos panel;
    // at the right edge unless moved, and large: 65% of the window across, 80% along, centred
    // (todosDefault), until the user sizes or places it
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
        // 25%, not the side panels' 15: the widest action, "[ Refresh (R) ]", whole inside the frame
        // on an 80-column screen; the action row's three at most fit whole from 60% there
        minimumSize: 25
        onResized: App.panelResized("todos", size, length)
        onOpened: App.panelOpened("todos")
        onClosed: App.panelClosed("todos")
        Flex {
            direction: Tui.Vertical
            // the task list's keys, live while the keyboard is in the list or its buttons: NOT in the
            // details pane below, whose read-only Editor lets the edit keys it refuses (a d s i o)
            // bubble, and a key read there must never change or remove the task being read
            FocusScope {
                Layout.fillHeight: true
                // ---- the todos's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
                Shortcut { sequence: "a"; onActivated: App.todoAdd() }
                Shortcut { sequence: "d"; onActivated: App.todoRemove() }
                Shortcut { sequence: "s"; onActivated: App.todoChooseStatus() }
                Shortcut { sequence: "i"; onActivated: App.todoToggleDetail() }
                Shortcut { sequence: "o"; onActivated: App.todoToggleDetail() }
                Shortcut { sequence: "Shift+R"; onActivated: App.todoRefresh() }
                Shortcut { sequence: "Shift+M"; onActivated: App.todoMove() }
                Shortcut { sequence: "Shift+L"; onActivated: App.todoLabel() }
                Shortcut { sequence: "Shift+A"; onActivated: App.todoAssign() }
                Shortcut { sequence: "?"; onActivated: todoKeys.open() }
                Shortcut { sequence: "1"; onActivated: App.todosTab(0) }
                Shortcut { sequence: "2"; onActivated: App.todosTab(1) }
                Shortcut { sequence: "3"; onActivated: App.todosTab(2) }
                Shortcut { sequence: "4"; onActivated: App.todosTab(3) }
                Shortcut { sequence: "5"; onActivated: App.todosTab(4) }
                // ---- end of the todos's keys ----
                Flex {
                    direction: Tui.Vertical
                    Flex {
                        direction: Tui.Horizontal
                        Button { text: "&New…"; onClicked: App.todoNew() }
                        Text { text: " " }
                        Button { text: "&Load…"; onClicked: App.todoLoad() }
                        Text { text: " " }
                        Button { text: "Keys (?)"; onClicked: todoKeys.open() }
                        Text { text: " " }
                        Button { text: "Close (&q)"; onClicked: todos.close() }
                    }
                    TreeView {
                        id: todoTree
                        Layout.fillHeight: true
                        palette.highlight: Theme.document.highlight
                        palette.highlightedText: Theme.document.highlightedText
                        model: App.todoTree
                        textRole: "label"
                        onCurrentIndexChanged: App.todoMoved(index)
                        onActivated: App.todoActivated(index)
                    }
                }
            }
            // the row under the cursor's details (i, o), Markdown, rendered as the pickers' previews
            // are (the GUI's Editor draws it; a terminal's shows the source): at most half the panel,
            // scrolled, so a long description never pushes the tree away
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
            // what needs the user: a file a section could not list; hidden while there is none
            Text { text: App.todosStatus; visible: App.todosStatusShown; wrapMode: Tui.WordWrap }
            // the action row: one line, only the actions that apply to the row under the cursor, each
            // its key's, the others hidden, never greyed; on a store not claimed none, only a note.
            // Which apply follows the cursor (todos.go: syncActions), never a button's own click.
            // the task list's keys again, live on these buttons too (the details pane above has none)
            FocusScope {
                // ---- the todos task buttons' keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----
                Shortcut { sequence: "a"; onActivated: App.todoAdd() }
                Shortcut { sequence: "d"; onActivated: App.todoRemove() }
                Shortcut { sequence: "s"; onActivated: App.todoChooseStatus() }
                Shortcut { sequence: "i"; onActivated: App.todoToggleDetail() }
                Shortcut { sequence: "o"; onActivated: App.todoToggleDetail() }
                Shortcut { sequence: "Shift+R"; onActivated: App.todoRefresh() }
                Shortcut { sequence: "Shift+M"; onActivated: App.todoMove() }
                Shortcut { sequence: "Shift+L"; onActivated: App.todoLabel() }
                Shortcut { sequence: "Shift+A"; onActivated: App.todoAssign() }
                Shortcut { sequence: "?"; onActivated: todoKeys.open() }
                Shortcut { sequence: "1"; onActivated: App.todosTab(0) }
                Shortcut { sequence: "2"; onActivated: App.todosTab(1) }
                Shortcut { sequence: "3"; onActivated: App.todosTab(2) }
                Shortcut { sequence: "4"; onActivated: App.todosTab(3) }
                Shortcut { sequence: "5"; onActivated: App.todosTab(4) }
                // ---- end of the todos task buttons' keys ----
                Flex {
                    direction: Tui.Horizontal
                    Text { text: "read-only until the cutover"; visible: App.todoReadOnly }
                    Button { text: "Add (a)"; visible: App.todoActAdd; enabled: App.todoActAdd; onClicked: App.todoAdd() }
                    Button { id: todoStatusButton; text: "Status… (s)"; visible: App.todoActStatus; enabled: App.todoActStatus; onClicked: App.todoChooseStatus() }
                    Text { text: " "; visible: App.todoActRefresh }
                    Button { id: todoRefreshButton; text: "Refresh (R)"; visible: App.todoActRefresh; enabled: App.todoActRefresh; onClicked: App.todoRefresh() }
                    Text { text: " "; visible: App.todoActLabel }
                    Button { text: "Label (L)"; visible: App.todoActLabel; enabled: App.todoActLabel; onClicked: App.todoLabel() }
                    Text { text: " "; visible: App.todoActAssign }
                    Button { text: "Assign (A)"; visible: App.todoActAssign; enabled: App.todoActAssign; onClicked: App.todoAssign() }
                    Text { text: " "; visible: App.todoActRemove }
                    Button { text: "Remove (d)"; visible: App.todoActRemove; enabled: App.todoActRemove; onClicked: App.todoRemove() }
                }
            }
        }
    }

    SearchPicker { id: searchPicker }
    Find { id: findDialog }
    FileOpen { id: fileOpen }
    OutlinePicker { id: outlinePicker }
    AgentAssign { id: agentAssignPicker }
    LuaPick { id: luaPicker }
    TodoStatus { id: todoStatusPicker }
    TerminalLook { id: terminalLook }
    FileName { id: fileName }
    OpenAnyFile { id: openAnyFile }
    NewAnyFile { id: newAnyFile }
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
    PluginKeys { id: pluginKeys }
    Help { id: help }
    Arrange { id: arrange }
    Tutorial { id: tutorial }
    About { id: about }
    TodoNew { id: todoNew }
    TodoLabel { id: todoLabel }
    TodoAssign { id: todoAssign }
    TodoFolder { id: todoFolder; title: "todos: load a store: its project's folder" }
    TodoFolder { id: todoFolderNew; title: "todos: a new store: the project's folder" }
    TodoFolder { id: todoFolderMove; title: "todos: move the store: its new folder" }
    Notifications { id: notifications }
    Confirm { id: confirm }
}
