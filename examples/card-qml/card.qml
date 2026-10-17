// card.qml — the People card as a golib QML layout. It names no colour: its palette is bound to
// Theme, which plugin/decl declares (autodoc.plugin 1.0) and keeps to AutoDoc's theme.
import tui 1.0
import autodoc.plugin 1.0
import people 1.0

Window {
    palette.window: Theme.app.window; palette.windowText: Theme.app.windowText
    palette.base: Theme.app.base; palette.text: Theme.app.text
    palette.highlight: Theme.app.highlight; palette.highlightedText: Theme.app.highlightedText

    Flex { direction: Tui.Vertical
        Text { text: "People" }
        Text { text: "add someone (Tab, then type, Enter)" }
        TextField { id: name; onAccepted: App.add(name.text) }
        Text { text: App.names }
        Text { text: App.status }
    }
}
