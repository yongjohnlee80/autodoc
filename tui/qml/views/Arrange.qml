// Arrange.qml — Go › Arrange panel (SPC L): the panel with the keyboard, moved and resized by keys
// (arrange.go). As the leader card, its body is TEXT, nothing focusable, so every key reaches its
// Shortcuts, and Escape reaches the card, not the drawer, which would close. The steps show at
// once; Enter keeps them, Esc (or closing it any other way) puts the panel back as it was.
Dialog {
    id: arrange
    title: App.arrangeTitle
    dim: false
    helpText: "Enter keeps · Esc puts it back"
    onClosed: App.arrangeCancel()
    Text { text: "h j k l, arrows   move it\nH J K L, Shift+arrows   resize it"; wrapMode: Tui.WordWrap }
    Shortcut { sequence: "h"; onActivated: App.arrangeStep("left") }
    Shortcut { sequence: "Left"; onActivated: App.arrangeStep("left") }
    Shortcut { sequence: "l"; onActivated: App.arrangeStep("right") }
    Shortcut { sequence: "Right"; onActivated: App.arrangeStep("right") }
    Shortcut { sequence: "k"; onActivated: App.arrangeStep("up") }
    Shortcut { sequence: "Up"; onActivated: App.arrangeStep("up") }
    Shortcut { sequence: "j"; onActivated: App.arrangeStep("down") }
    Shortcut { sequence: "Down"; onActivated: App.arrangeStep("down") }
    Shortcut { sequence: "Shift+H"; onActivated: App.arrangeStep("narrower") }
    Shortcut { sequence: "Shift+Left"; onActivated: App.arrangeStep("narrower") }
    Shortcut { sequence: "Shift+L"; onActivated: App.arrangeStep("wider") }
    Shortcut { sequence: "Shift+Right"; onActivated: App.arrangeStep("wider") }
    Shortcut { sequence: "Shift+K"; onActivated: App.arrangeStep("shorter") }
    Shortcut { sequence: "Shift+Up"; onActivated: App.arrangeStep("shorter") }
    Shortcut { sequence: "Shift+J"; onActivated: App.arrangeStep("taller") }
    Shortcut { sequence: "Shift+Down"; onActivated: App.arrangeStep("taller") }
    Shortcut { sequence: "Return"; onActivated: { App.arrangeKeep(); arrange.close() } }
}
