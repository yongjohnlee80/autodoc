// Arrange.qml — Go › Arrange panel (SPC L): the panel with the keyboard, moved and resized by keys
// (arrange.go). As the leader card, its body is TEXT, nothing focusable, so every key reaches its
// Shortcuts, and Escape reaches the card, not the drawer, which would close. The steps show at
// once; Enter keeps them, Esc (or closing it any other way) puts the panel back as it was.
Dialog {
    id: arrange
    closeOnQ: true
    keyScope: "autodoc.arrange" // its keys' commands (tui/keys.go: dialogCommands)
    title: App.arrangeTitle
    dim: false
    helpText: "Enter keeps · q or Esc puts it back"
    onClosed: App.arrangeCancel()
    Text { text: "h j k l, arrows   move it\nH J K L, Shift+arrows   resize it"; wrapMode: Tui.WordWrap }
    Shortcut { sequence: keyId("autodoc.arrange.left"); onActivated: App.arrangeStep("left") }
    Shortcut { sequence: keyId("autodoc.arrange.right"); onActivated: App.arrangeStep("right") }
    Shortcut { sequence: keyId("autodoc.arrange.up"); onActivated: App.arrangeStep("up") }
    Shortcut { sequence: keyId("autodoc.arrange.down"); onActivated: App.arrangeStep("down") }
    Shortcut { sequence: keyId("autodoc.arrange.narrower"); onActivated: App.arrangeStep("narrower") }
    Shortcut { sequence: keyId("autodoc.arrange.wider"); onActivated: App.arrangeStep("wider") }
    Shortcut { sequence: keyId("autodoc.arrange.shorter"); onActivated: App.arrangeStep("shorter") }
    Shortcut { sequence: keyId("autodoc.arrange.taller"); onActivated: App.arrangeStep("taller") }
    Shortcut { sequence: keyId("autodoc.arrange.keep"); onActivated: { App.arrangeKeep(); arrange.close() } }
}
