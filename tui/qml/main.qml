// main.qml — AutoDoc's screen, in QML.
//
// STRUCTURE ONLY: what is on the screen, where it is docked, and what each control triggers. It
// names no colour (the imported theme does) and runs nothing itself: every action is an App
// command, and the host decides what it does.
//
// A writing screen: the note is a page, as wide as reading wants (App.pageWidth, the ruler's
// columns and its border) and centred, and nothing else shows until it is asked for. The menu bar
// hides until F10 or an Alt+letter brings it up; the status line shows when its preference says;
// the explorer and the links are drawers over the page, which never moves under them.

import tui 1.0
import autodoc 1.0                // App: this program's state and commands
import autodoc.theme.dark 1.0     // the Theme singleton; View › Theme switches it
import autodoc.dialogs 1.0        // the pickers, the note, workspace, preference and quit dialogs
import autodoc.views 1.0          // Help, About, the leader card

Window {
    palette.window: Theme.app.window
    palette.windowText: Theme.app.windowText
    palette.button: Theme.app.button
    palette.buttonText: Theme.app.buttonText
    palette.highlight: Theme.app.highlight
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
    // the Text keymap, type them. Ctrl+Space is the leader in every keymap and mode.
    Shortcut { sequence: "Space"; onActivated: leader.open() }
    Shortcut { sequence: "Ctrl+Space"; onActivated: leader.open() }
    Shortcut { sequence: "Ctrl+H"; onActivated: App.movePane("h") }
    Shortcut { sequence: "Ctrl+J"; onActivated: App.movePane("j") }
    Shortcut { sequence: "Ctrl+K"; onActivated: App.movePane("k") }
    Shortcut { sequence: "Ctrl+L"; onActivated: App.movePane("l") }
    Shortcut { sequence: "Ctrl+S"; onActivated: App.save() }
    Shortcut { sequence: "Ctrl+O"; onActivated: App.openPicker() }
    Shortcut { sequence: "Ctrl+N"; onActivated: App.newNote() }
    // Ctrl+G, and / where the editor leaves it (Normal mode), as vim's search: the Vim keyset takes
    // Ctrl+F and Ctrl+B to page
    Shortcut { sequence: "Ctrl+G"; onActivated: App.openSearch() }
    Shortcut { sequence: "/"; onActivated: App.openSearch() }
    Shortcut { sequence: "Ctrl+W"; onActivated: App.pickWorkspace() }
    Shortcut { sequence: "Ctrl+Q"; onActivated: App.quit() }
    Shortcut { sequence: "F1"; onActivated: help.open() }

    MenuBar {
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
            MenuItem { text: "&New note…"; onTriggered: App.newNote() }
            MenuItem { text: "&Open note…"; onTriggered: App.openPicker() }
            MenuItem { text: "&Save"; onTriggered: App.save() }
            MenuItem { text: "&Reload from disk"; onTriggered: App.reload() }
            MenuItem { text: "E&xit"; onTriggered: App.quit() }
        }
        Menu {
            title: "&Go"
            MenuItem { text: "&Search…"; onTriggered: App.openSearch() }
            MenuItem { text: "&Explorer"; onTriggered: App.toggleExplorer() }
            MenuItem { text: "&Links"; onTriggered: App.toggleLinks() }
            MenuItem { text: "&Workspace…"; onTriggered: App.pickWorkspace() }
            MenuItem { text: "&Manage workspaces…"; onTriggered: App.manageWorkspaces() }
        }
        Menu {
            title: "&View"
            MenuItem { text: "&Status line"; checkable: true; checked: App.statusShown; onTriggered: App.toggleStatusLine() }
            MenuItem { text: "&Hide the menu bar"; checkable: true; checked: App.menuAutoHide; onTriggered: App.toggleMenuBar() }
        }
        Menu {
            title: "&Options"
            Menu {
                title: "&Keymap"
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
            }
            MenuItem { text: "&Editor preferences…"; onTriggered: App.openPrefs() }
            MenuItem { text: "&AI models…"; onTriggered: App.openAIModels() }
        }
        Menu {
            title: "&Help"
            align: Tui.Right
            MenuItem { text: "&Keys"; onTriggered: help.open() }
            MenuItem { text: "&About"; onTriggered: about.open() }
        }
    }

    // ---- the page: the note, centred, as wide as the ruler ----
    Frame {
        title: App.noteTitle
        maximumWidth: App.pageWidth
        palette.window: Theme.document.window
        palette.windowText: Theme.document.windowText
        palette.highlight: Theme.document.highlight
        palette.highlightedText: Theme.document.highlightedText
        palette.base: Theme.document.base
        palette.text: Theme.document.text
        Editor {
            id: editor
            focus: true
            palette.highlight: Theme.document.selection
            palette.highlightedText: Theme.document.selectedText
            keyset: App.keyset
            ruler: App.rulerColumn
            onModeChanged: App.syncMode()
            onTextChanged: App.edited()
            SyntaxHighlighter { definition: "Markdown" }
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
        // semantic search: a green dot while it answers, a red one while the search is by words
        Text { text: App.semanticMark; color: App.semanticDot }
        Text { text: App.semanticLabel }
    }

    // ---- the panels: drawers over the page, each from the edge its preference names ----
    Drawer {
        id: explorer
        modal: false
        edge: App.explorerEdge
        size: 30
        length: App.explorerLength
        onOpened: App.panelOpened("explorer")
        onClosed: App.panelClosed("explorer")
        Frame {
            title: "explorer"
            TreeView {
                id: explorerTree
                palette.highlight: Theme.document.highlight
                palette.highlightedText: Theme.document.highlightedText
                model: App.explorer
                textRole: "label"
                onActivated: App.explorerActivated(index)
            }
        }
    }
    Drawer {
        id: links
        modal: false
        edge: App.linksEdge
        size: 30
        length: App.linksLength
        onOpened: App.panelOpened("links")
        onClosed: App.panelClosed("links")
        Frame {
            title: App.linksTitle
            ListView {
                id: linksList
                palette.highlight: Theme.document.highlight
                palette.highlightedText: Theme.document.highlightedText
                model: App.backlinks
                textRole: "label"
                onActivated: App.openBacklink(index)
            }
        }
    }

    SearchPicker { id: searchPicker }
    NoteOpen { id: noteOpen }
    NoteName { id: noteName }
    UnsavedNote { id: unsavedNote }
    NoteConflict { id: noteConflict }
    WorkspacePicker { id: workspacePicker }
    WorkspaceManager { id: workspaceManager }
    WorkspaceAdd { id: workspaceAdd }
    WorkspaceRename { id: workspaceRename }
    WorkspaceRemove { id: workspaceRemove }
    Preferences { id: preferences }
    AIModels { id: aiModels }
    ProviderEdit { id: providerEdit }
    ProviderRemove { id: providerRemove }
    ConfirmQuit { id: confirmQuit }
    Leader { id: leader }
    Help { id: help }
    About { id: about }
}
