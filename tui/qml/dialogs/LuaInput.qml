// LuaInput.qml — a script's one-line question (autodoc.ui.input, luahost.go): its title, what it
// asks and the line, all the script's. Enter answers the script with the text; Esc, or any other
// close, answers it "cancelled".
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    title: App.luaInputTitle
    width: 64
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: App.luaInputLabel }
        TextField { id: luaInputField; text: App.luaInputText; onAccepted: App.luaInputSet(text) }
    }
    onRejected: App.luaInputCancel()
    onClosed: App.luaInputClosed()
}
