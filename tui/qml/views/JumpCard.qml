// JumpCard.qml — SPC j: the open document's first nine neighbours from the Relations drawer, a
// digit each (relations.go). As the leader card, its body is TEXT, nothing focusable, so every
// digit reaches its Shortcut. Esc closes it.
Dialog {
    id: jumpCard
    title: "related documents"
    dim: false
    helpText: "1–9 opens one · Esc closes"
    Text { text: App.jumpText; wrapMode: Tui.WordWrap }
    Shortcut { sequence: "1"; onActivated: App.jumpTo(1) }
    Shortcut { sequence: "2"; onActivated: App.jumpTo(2) }
    Shortcut { sequence: "3"; onActivated: App.jumpTo(3) }
    Shortcut { sequence: "4"; onActivated: App.jumpTo(4) }
    Shortcut { sequence: "5"; onActivated: App.jumpTo(5) }
    Shortcut { sequence: "6"; onActivated: App.jumpTo(6) }
    Shortcut { sequence: "7"; onActivated: App.jumpTo(7) }
    Shortcut { sequence: "8"; onActivated: App.jumpTo(8) }
    Shortcut { sequence: "9"; onActivated: App.jumpTo(9) }
}
