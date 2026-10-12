// SearchPicker.qml — Go › Search (Ctrl+G, /): the picker over a search. On the left the search, a
// row saying what an unanswered search waits on, then its hits, a section a row, each with its
// relevance on the left; on the right the note under the cursor, at that section, the search's
// words marked, and its Rendered view for a Markdown hit. In the footer, a checkbox for each stage
// the search can run: Lexical, Semantic while a model is in use, Rerank while a ranker is; a change
// searches again. Enter opens the hit, the cursor at its section; q closes, outside the field.
// Ctrl+h/j/k/l move between the parts. Its handlers are Picker's, App.pick*("search", …).
Picker {
    name: "search"
    title: App.searchTitle
    label: "search: words; a * ends a prefix"
    fieldText: App.searchText
    model: App.hits
    // blank, or a turning spinner and what an unanswered search is waiting on
    note: App.searchStatus
    noteShown: true
    noteColor: Theme.document.lineNumber
    listTitle: App.hitsTitle
    previewTitle: App.searchPreviewTitle
    previewText: App.searchPreviewText
    previewAt: App.searchPreviewAt
    rendered: App.searchPreviewRendered
    syntax: "Markdown (search)"
    TableViewColumn { role: "hit"; title: "HIT"; width: 4 }
    TableViewColumn { role: "path"; title: "FILE"; width: 0; elideMode: Tui.ElidePath }
    TableViewColumn { role: "section"; title: "SECTION"; width: 22 }
    // the stages the search runs: a box for each the daemon can, checked or not; one of Lexical
    // and Semantic always stays checked
    footer: Flex {
        direction: Tui.Horizontal
        CheckBox { text: "Lexical"; checked: App.stageLexical; onToggled: App.toggleSearchStage("lexical") }
        Text { text: "  " }
        CheckBox { text: "Semantic"; checked: App.stageSemantic; visible: App.stageSemanticShown; onToggled: App.toggleSearchStage("semantic") }
        Text { text: "  "; visible: App.stageRerankShown }
        CheckBox { text: App.stageRerankText; checked: App.stageRerank; visible: App.stageRerankShown; onToggled: App.toggleSearchStage("rerank") }
    }
}
