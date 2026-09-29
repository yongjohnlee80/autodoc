// main.qml — AutoDoc's screen, in QML.
//
// STRUCTURE ONLY: what is on the screen, where it is docked, and what each control triggers. It
// names no colour (the imported theme does) and runs nothing itself: every action is an App
// command, and the host decides what it does.
//
// A palette is set ONCE, where it starts: the Window carries the application palette; a surface
// that is a distinct part of the design (the menu bar, the document, the status line) overrides
// only its own roles; everything else inherits.

import tui 1.0
import autodoc 1.0                // App: this program's state and commands
import autodoc.theme.dark 1.0     // the Theme singleton; View › Theme switches it
import autodoc.dialogs 1.0        // the note, search, workspace, workspace manager and quit dialogs
import autodoc.views 1.0          // Help, About

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

    // ---- keys: they fire whichever widget has focus, when it leaves them ----
    Shortcut { sequence: "Ctrl+S"; onActivated: App.save() }
    Shortcut { sequence: "Ctrl+O"; onActivated: App.openPicker() }
    Shortcut { sequence: "Ctrl+N"; onActivated: App.newNote() }
    // Ctrl+G, and / where the editor leaves it (Normal mode), as vim's search: the Vim keyset takes
    // Ctrl+F and Ctrl+B to page
    Shortcut { sequence: "Ctrl+G"; onActivated: searchDialog.open() }
    Shortcut { sequence: "/"; onActivated: searchDialog.open() }
    Shortcut { sequence: "Ctrl+W"; onActivated: App.pickWorkspace() }
    Shortcut { sequence: "Ctrl+Q"; onActivated: App.quit() }
    Shortcut { sequence: "F1"; onActivated: help.open() }
    Shortcut { sequence: "Alt+1"; onActivated: notes.forceActiveFocus() }
    Shortcut { sequence: "Alt+2"; onActivated: editor.forceActiveFocus() }
    Shortcut { sequence: "Alt+3"; onActivated: links.forceActiveFocus() }

    MenuBar {
        Dock.edge: Tui.Top
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
            MenuItem { text: "&Search…"; onTriggered: searchDialog.open() }
            MenuItem { text: "All &notes"; onTriggered: App.listNotes() }
            MenuItem { text: "&Workspace…"; onTriggered: App.pickWorkspace() }
            MenuItem { text: "&Manage workspaces…"; onTriggered: App.manageWorkspaces() }
        }
        Menu {
            title: "&View"
            Menu {
                title: "&Theme"
                MenuItem { text: "&Dark";  group: "theme"; checked: App.themeDark;  onTriggered: App.useTheme("dark") }
                MenuItem { text: "&Light"; group: "theme"; checked: App.themeLight; onTriggered: App.useTheme("light") }
                MenuItem { text: "&Mono";  group: "theme"; checked: App.themeMono;  onTriggered: App.useTheme("mono") }
                MenuItem { text: "&Retro"; group: "theme"; checked: App.themeRetro; onTriggered: App.useTheme("retro") }
            }
        }
        Menu {
            title: "&Help"
            align: Tui.Right
            MenuItem { text: "&Keys"; onTriggered: help.open() }
            MenuItem { text: "&About"; onTriggered: about.open() }
        }
    }

    // ---- the body: the notes (or the hits) on the left; the note and its backlinks on the right ----
    Split {
        orientation: Tui.Horizontal
        ratio: 0.3
        Frame {
            title: App.resultsTitle
            TableView {
                id: notes
                palette.highlight: Theme.document.highlight
                palette.highlightedText: Theme.document.highlightedText
                model: App.results
                onActivated: App.openResult(index)
                TableViewColumn { role: "path"; title: "NOTE"; width: 0 }
            }
        }
        Split {
            orientation: Tui.Vertical
            ratio: 0.75
            Frame {
                title: App.noteTitle
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
                    readOnly: App.noNote
                    onModeChanged: App.syncMode()
                    onTextChanged: App.edited()
                }
            }
            Frame {
                title: App.linksTitle
                ListView {
                    id: links
                    palette.highlight: Theme.document.highlight
                    palette.highlightedText: Theme.document.highlightedText
                    model: App.backlinks
                    textRole: "label"
                    onActivated: App.openBacklink(index)
                }
            }
        }
    }

    StatusBar {
        Dock.edge: Tui.Bottom
        palette.window: Theme.status.window
        palette.windowText: Theme.status.windowText
        left: App.statusLeft
        center: App.statusCenter
        right: App.status
    }

    SearchDialog { id: searchDialog }
    NoteOpen { id: noteOpen }
    NoteName { id: noteName }
    UnsavedNote { id: unsavedNote }
    NoteConflict { id: noteConflict }
    WorkspacePicker { id: workspacePicker }
    WorkspaceManager { id: workspaceManager }
    WorkspaceAdd { id: workspaceAdd }
    WorkspaceRename { id: workspaceRename }
    WorkspaceRemove { id: workspaceRemove }
    ConfirmQuit { id: confirmQuit }
    Help { id: help }
    About { id: about }
}
