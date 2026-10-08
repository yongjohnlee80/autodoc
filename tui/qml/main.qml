// main.qml — AutoDoc's screen, in QML.
//
// STRUCTURE ONLY: what is on the screen, where it is docked, and what each control triggers. It
// names no colour (the imported theme does) and runs nothing itself: every action is an App
// command, and the host decides what it does.
//
// A writing screen: the file is a page, as wide as reading wants (App.pageWidth, the ruler's
// columns and its border) and centred, and nothing else shows until it is asked for. The menu bar
// hides until F10 or an Alt+letter brings it up; the status line shows when its preference says;
// the explorer and the links are drawers over the page, which never moves under them.

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

    // ---- keys: they fire whichever widget has focus, when it leaves them ----
    // Space and Ctrl+h/j/k/l reach here from the page only in Vim's Normal mode: Insert mode, and
    // the Text editor mode, type them. Ctrl+Space is the leader in every editor mode.
    Shortcut { sequence: "Space"; onActivated: leader.open() }
    Shortcut { sequence: "Ctrl+Space"; onActivated: leader.open() }
    Shortcut { sequence: "Ctrl+H"; onActivated: App.movePane("h") }
    Shortcut { sequence: "Ctrl+J"; onActivated: App.movePane("j") }
    Shortcut { sequence: "Ctrl+K"; onActivated: App.movePane("k") }
    Shortcut { sequence: "Ctrl+L"; onActivated: App.movePane("l") }
    Shortcut { sequence: "Ctrl+S"; onActivated: App.save() }
    Shortcut { sequence: "Ctrl+O"; onActivated: App.openPicker() }
    Shortcut { sequence: "Ctrl+N"; onActivated: App.newFile() }
    // Ctrl+G, and / where the editor leaves it (Normal mode), as vim's search: the Vim keyset takes
    // Ctrl+F and Ctrl+B to page
    Shortcut { sequence: "Ctrl+G"; onActivated: App.openSearch() }
    // / finds in the pane with the keyboard; n and N find again (find.go)
    Shortcut { sequence: "/"; onActivated: App.openFind() }
    Shortcut { sequence: "n"; onActivated: App.findNext() }
    Shortcut { sequence: "Shift+N"; onActivated: App.findPrevious() }
    // d in the Relations drawer shows the second ring, or hides it (relations.go); elsewhere nothing
    Shortcut { sequence: "d"; onActivated: App.relationsDepth() }
    Shortcut { sequence: "Ctrl+W"; onActivated: App.pickWorkspace() }
    Shortcut { sequence: "Ctrl+Q"; onActivated: App.quit() }
    Shortcut { sequence: "F1"; onActivated: help.open() }
    // ? is the Vim keys' card, in Normal mode (the Text mode types it); ? again closes it
    Shortcut { sequence: "?"; onActivated: App.toggleVimKeys() }

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
            MenuItem { text: "&Links"; onTriggered: App.toggleLinks() }
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
        }
        Menu {
            title: "&View"
            MenuItem { text: "&Status line"; checkable: true; checked: App.statusShown; onTriggered: App.toggleStatusLine() }
            MenuItem { text: "&Hide the menu bar"; checkable: true; checked: App.menuAutoHide; onTriggered: App.toggleMenuBar() }
            MenuSeparator {}
            MenuItem { text: "&Wrap long lines"; checkable: true; checked: App.editorWrap; onTriggered: App.toggleWrap() }
            MenuItem { text: "Line &numbers"; checkable: true; checked: App.lineNumbers; onTriggered: App.toggleLineNumbers() }
            MenuSeparator {}
            MenuItem { text: "&Image previews"; checkable: true; checked: App.imagePreviews; onTriggered: App.toggleImagePreviews() }
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
        Editor {
            id: editor
            contextMenu: true   // right-click: Undo, Redo, Copy, Cut, Paste
            Layout.fillHeight: true
            focus: true
            palette.highlight: Theme.document.selection
            palette.highlightedText: Theme.document.selectedText
            keyset: App.keyset
            ruler: App.rulerColumn
            wrap: App.editorWrap
            lineNumbers: App.lineNumbers
            // the theme's accent for the cursor, and its dim tone for the line numbers
            cursorColor: Theme.document.cursor
            lineNumberColor: Theme.document.lineNumber
            onModeChanged: App.syncMode()
            onTextChanged: App.edited()
            onCursorPositionChanged: App.cursorMoved()
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

    // ---- the panels: drawers over the page, each from the edge its preference names ----
    Drawer {
        id: explorer
        modal: false
        edge: App.explorerEdge
        size: App.explorerSize
        length: App.explorerLength
        // floats where it is dragged or arranged (panelfloat.go): Alt/Option-left drag moves it,
        // Alt/Option-right drag resizes it; until then it is at its edge
        movable: true
        windowResize: true
        floating: App.explorerFloating
        floatX: App.explorerX; floatY: App.explorerY; floatWidth: App.explorerW; floatHeight: App.explorerH
        onPlaced: App.panelPlaced("explorer", x, y, width, height)
        minimumSize: 15
        onResized: App.panelResized("explorer", size, length)
        onOpened: App.panelOpened("explorer")
        onClosed: App.panelClosed("explorer")
        Frame {
            title: "explorer"
            // on the page's colour, as the list in it is: a frame's own window tone would show as
            // a band beside its border, which the GUI draws as a thin line through the cell
            palette.window: Theme.document.base
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
    // the native HTML preview (File › Preview HTML under --gui): the note's page beside the editor,
    // live as it is edited and following the cursor (htmlpane.go); a terminal never opens it
    Drawer {
        id: htmlPane
        modal: false
        edge: Tui.Right
        size: 50
        length: 100
        movable: true
        windowResize: true
        floating: App.htmlPaneFloating
        floatX: App.htmlPaneX; floatY: App.htmlPaneY; floatWidth: App.htmlPaneW; floatHeight: App.htmlPaneH
        onPlaced: App.panelPlaced("htmlPane", x, y, width, height)
        minimumSize: 20
        onOpened: App.panelOpened("htmlPane")
        onClosed: App.panelClosed("htmlPane")
        Frame {
            title: "preview"
            palette.window: Theme.document.base
            HTMLView {
                id: htmlView
                onLinkActivated: App.htmlLink(href)
            }
        }
    }
    Drawer {
        id: links
        modal: false
        edge: App.linksEdge
        size: App.linksSize
        length: App.linksLength
        // floats where it is dragged or arranged (panelfloat.go): Alt/Option-left drag moves it,
        // Alt/Option-right drag resizes it; until then it is at its edge
        movable: true
        windowResize: true
        floating: App.linksFloating
        floatX: App.linksX; floatY: App.linksY; floatWidth: App.linksW; floatHeight: App.linksH
        onPlaced: App.panelPlaced("links", x, y, width, height)
        minimumSize: 15
        onResized: App.panelResized("links", size, length)
        onOpened: App.panelOpened("links")
        onClosed: App.panelClosed("links")
        Frame {
            title: App.relationsTitle
            // on the page's colour, as the list in it is: a frame's own window tone would show as
            // a band beside its border, which the GUI draws as a thin line through the cell
            palette.window: Theme.document.base
            ListView {
                id: linksList
                palette.highlight: Theme.document.highlight
                palette.highlightedText: Theme.document.highlightedText
                model: App.relations
                textRole: "label"
                currentIndex: App.linksIndex
                onCurrentIndexChanged: App.linksMoved(index)
                onActivated: App.openRelation(index)
            }
        }
    }
    // the terminal: a shell in the workspace's folder, from the edge (or the centre) its preference
    // names, at that place's size; it keeps running while hidden (terminal.go)
    Drawer {
        id: terminal
        modal: false
        edge: App.terminalEdge
        size: App.terminalSize
        length: App.terminalLength
        // floats where it is dragged or arranged (panelfloat.go): Alt/Option-left drag moves it,
        // Alt/Option-right drag resizes it; until then it is at its edge
        movable: true
        windowResize: true
        floating: App.terminalFloating
        floatX: App.terminalX; floatY: App.terminalY; floatWidth: App.terminalW; floatHeight: App.terminalH
        onPlaced: App.panelPlaced("terminal", x, y, width, height)
        onResized: App.panelResized("terminal", size, length)
        onOpened: App.panelOpened("terminal")
        onClosed: App.panelClosed("terminal")
        Frame {
            title: "terminal"
            Terminal {
                id: terminalView
                dir: App.terminalDir
                vimKeys: App.keymapVim
                // a program's colours raised until they read on the page: ls's yellow and a
                // prompt's cyan are written for a dark terminal, and wash out on a light theme
                minimumContrast: 4.5
                onExited: App.terminalExited(code)
            }
        }
    }

    // the agent: an AI agent's CLI, as its default profile says, floating over the page; it keeps
    // running while hidden (agent.go)
    Drawer {
        id: agent
        modal: false
        edge: App.agentEdge
        size: App.agentSize
        length: App.agentLength
        // floats where it is dragged or arranged (panelfloat.go): Alt/Option-left drag moves it,
        // Alt/Option-right drag resizes it; until then it is at its edge
        movable: true
        windowResize: true
        floating: App.agentFloating
        floatX: App.agentX; floatY: App.agentY; floatWidth: App.agentW; floatHeight: App.agentH
        onPlaced: App.panelPlaced("agent", x, y, width, height)
        onOpened: App.panelOpened("agent")
        onClosed: App.panelClosed("agent")
        onResized: App.panelResized("agent", size, length)
        Frame {
            title: App.agentTitle
            Terminal {
                id: agentView
                vimKeys: App.keymapVim
                // a program's colours raised until they read on the page: ls's yellow and a
                // prompt's cyan are written for a dark terminal, and wash out on a light theme
                minimumContrast: 4.5
                onExited: App.agentExited(code)
            }
        }
    }

    SearchPicker { id: searchPicker }
    Find { id: findDialog }
    FileOpen { id: fileOpen }
    OutlinePicker { id: outlinePicker }
    FileName { id: fileName }
    OpenAnyFile { id: openAnyFile }
    NewAnyFile { id: newAnyFile }
    UnsavedFile { id: unsavedFile }
    OpenHTML { id: openHTML }
    FileConflict { id: fileConflict }
    WorkspacePicker { id: workspacePicker }
    WorkspaceManager { id: workspaceManager }
    WorkspaceSettings { id: workspaceSettings }
    WorkspaceRoot { id: workspaceRoot }
    WorkspaceRemove { id: workspaceRemove }
    PluginAdd { id: pluginAdd }
    PluginConfirm { id: pluginConfirm }
    PluginManager { id: pluginManager }
    PluginRemove { id: pluginRemove }
    Preferences { id: preferences }
    Diagram { id: diagram }
    HtmlPreview { id: htmlPreview }
    AIModels { id: aiModels }
    ProviderEdit { id: providerEdit }
    ProviderRemove { id: providerRemove }
    RankerEdit { id: rankerEdit }
    Recent { id: recent }
    AgentProfiles { id: agentProfiles }
    AgentEdit { id: agentEdit }
    AgentSwitch { id: agentSwitch }
    RankerRemove { id: rankerRemove }
    RankerWindow { id: rankerWindow }
    RestartBackend { id: restartBackend }
    Mismatch { id: mismatch }
    Registrations { id: registrations }
    Vectors { id: vectorsDialog }
    PurgeModel { id: purgeModel }
    ConfirmQuit { id: confirmQuit }
    Leader { id: leader }
    JumpCard { id: jumpCard }
    PluginKeys { id: pluginKeys }
    Help { id: help }
    Arrange { id: arrange }
    About { id: about }
    Notifications { id: notifications }
}
