// card.qml — a reading list in two panes (plugin/decl's Panes and Pane): the list and its field
// beside a status pane with nothing to type into, which is a stop of its own. F7 and Shift+F7 move
// between them, F8 maximizes one, Tab stays in a pane. Its palette is bound to Theme, so it follows
// AutoDoc's theme.
import tui 1.0
import autodoc.plugin 1.0
import reading 1.0

Window {
    palette.window: Theme.app.window; palette.windowText: Theme.app.windowText
    palette.base: Theme.app.base; palette.text: Theme.app.text
    palette.highlight: Theme.app.highlight; palette.highlightedText: Theme.app.highlightedText

    Panes {
        orientation: Tui.Horizontal
        ratio: 0.6
        Pane { title: "Reading list"
            Flex { direction: Tui.Vertical
                Text { text: App.books }
                Text { text: "add a book (type, Enter)" }
                TextField { id: book; onAccepted: App.add(book.text) }
            }
        }
        Pane { title: "Status"; Text { text: App.status } }
    }
}
