// Recovery.qml — unsaved work AutoDoc kept when it last ended without being asked to (a signal, the
// terminal closing): restore it, discard the copy, or ask again at the next start. Restore takes
// initial focus; Discard needs its letter, so a stray Enter cannot throw the copy away.
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: "unsaved work kept"
    Text { wrapMode: Tui.WordWrap; text: App.recoveryQuestion }
    DialogButtonBox {
        Button { text: "&Restore"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole; onClicked: App.recovery("restore") }
        Button { text: "&Discard"; DialogButtonBox.buttonRole: DialogButtonBox.DestructiveRole; onClicked: App.recovery("discard") }
        Button { text: "&Later";   DialogButtonBox.buttonRole: DialogButtonBox.RejectRole; onClicked: App.recovery("later") }
    }
}
