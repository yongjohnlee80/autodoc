// BufferCard.qml — a card of keys, as AutoDB's: every command here, by key. GENERATED from
// tui/keys.go (go test ./tui -run TestKeysAreGenerated -update): edit the catalog there, not this
// file. The rows are TEXT, not a list: a list takes the keyboard and eats letters that are keys
// here. With nothing focusable in it, every key reaches the dialog's Shortcuts, one a command, and
// each closes the card before its command runs, so what the command opens is not under it. Esc
// closes it.
Dialog {
    id: buffer
    closeOnQ: true
    keyScope: "autodoc.buffer"
    title: qsTrId("autodoc.card.buffer.title")
    dim: false
    helpText: qsTrId("autodoc.card.buffer.keyhelp")
    Text { text: App.bufferText; wrapMode: Tui.WordWrap } // WordWrap keeps its lines: NoWrap is one
    Shortcut { sequence: "Backspace"; onActivated: { buffer.close(); leader.open() } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.1"); onActivated: { buffer.close(); App.showBufferAt(1) } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.2"); onActivated: { buffer.close(); App.showBufferAt(2) } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.3"); onActivated: { buffer.close(); App.showBufferAt(3) } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.4"); onActivated: { buffer.close(); App.showBufferAt(4) } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.5"); onActivated: { buffer.close(); App.showBufferAt(5) } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.6"); onActivated: { buffer.close(); App.showBufferAt(6) } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.7"); onActivated: { buffer.close(); App.showBufferAt(7) } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.8"); onActivated: { buffer.close(); App.showBufferAt(8) } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.9"); onActivated: { buffer.close(); App.showBufferAt(9) } }
    Shortcut { sequence: keyId("autodoc.buffer.showBufferAt.10"); onActivated: { buffer.close(); App.showBufferAt(10) } }
    Shortcut { sequence: keyId("autodoc.buffer.closeBuffer"); onActivated: { buffer.close(); App.closeBuffer() } }
    Shortcut { sequence: keyId("autodoc.buffer.openBuffers"); onActivated: { buffer.close(); App.openBuffers() } }
    Shortcut { sequence: keyId("autodoc.buffer.closeBuffersRight"); onActivated: { buffer.close(); App.closeBuffersRight() } }
    Shortcut { sequence: keyId("autodoc.buffer.closeBuffersLeft"); onActivated: { buffer.close(); App.closeBuffersLeft() } }
    Shortcut { sequence: keyId("autodoc.buffer.goBack"); onActivated: { buffer.close(); App.goBack() } }
}
