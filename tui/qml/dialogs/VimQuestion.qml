// VimQuestion.qml — the first run's first card (vimquestion.go): does the user use Vim? Yes keeps
// Vim's modal keys; No, and Escape, are a text editor's, whose terminals never stay in normal mode
// unasked. The agent setup's card follows. Preferences change it later.
Dialog {
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: qsTrId("autodoc.dialog.vimQuestion.do_you_use_vim")
    width: 64
    dim: true
    Flex {
        direction: Tui.Vertical
        Text { wrapMode: Tui.WordWrap; text: qsTrId("autodoc.dialog.vimQuestion.autodoc_s_editors_and_terminals_can_work_the_vim") }
        Text { wrapMode: Tui.WordWrap; text: qsTrId("autodoc.dialog.vimQuestion.editor_preferences_editor_mode_changes_it_at_any") }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.vimQuestion.no_a_text_editor_s_keys"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: qsTrId("autodoc.dialog.vimQuestion.yes_vim_s_keys"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
    onAccepted: App.answerVimQuestion("vim")
    onRejected: App.answerVimQuestion("text")
}
