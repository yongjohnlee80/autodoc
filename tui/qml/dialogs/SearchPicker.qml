// SearchPicker.qml — Go › Search (Ctrl+G, /): the picker's layout. On the left the search, then its
// hits, a section a row, each with its relevance on the left; on the right the note under the cursor, at that section, the search's
// words marked. Beneath both, a checkbox for each stage the search can run: Lexical, Semantic
// while a model is in use, Rerank while a ranker is; a change searches again. Enter opens the hit,
// the cursor at its section; q closes, outside the field. Ctrl+h/j/k/l move between the parts.
Dialog {
    closeOnQ: true
    maxWidthPercent: 92
    maxHeightPercent: 90
    title: App.searchTitle
    dim: false
    Flex {
        direction: Tui.Vertical
        Split {
            Layout.fillHeight: true
            orientation: Tui.Horizontal
            ratio: 0.42
            Flex {
                direction: Tui.Vertical
                Text { text: "search: words; a * ends a prefix" }
                TextField {
                    id: query
                    text: App.searchText
                    onTextEdited: App.searchLive(text)
                    onAccepted: App.openHit(hits.currentIndex)
                }
                // a row between the field and the hits: blank, or a turning spinner and what an
                // unanswered search is waiting on
                Text { text: App.searchStatus; color: Theme.document.lineNumber }
                Frame {
                    title: App.hitsTitle
                    Layout.fillHeight: true
                    TableView {
                        id: hits
                        palette.highlight: Theme.document.highlight
                        palette.highlightedText: Theme.document.highlightedText
                        model: App.hits
                        onCurrentIndexChanged: App.previewHit(index)
                        onActivated: App.openHit(index)
                        TableViewColumn { role: "hit"; title: "HIT"; width: 4 }
                        TableViewColumn { role: "path"; title: "FILE"; width: 0; elideMode: Tui.ElidePath }
                        TableViewColumn { role: "section"; title: "SECTION"; width: 22 }
                    }
                }
            }
            Frame {
                title: App.searchPreviewTitle
                Editor {
                    id: searchPreview
                    readOnly: true
                    text: App.searchPreviewText
                    cursorPosition: App.searchPreviewAt
                    renderedEnabled: App.searchPreviewRendered
                    SyntaxHighlighter { definition: "Markdown (search)" }
                    // its Rendered view (Ctrl+T, or the title bar's switch), for a Markdown hit: the
                    // GUI's Editor draws it; the terminal's has none and ignores it
                    MarkdownRenderer {}
                }
            }
        }
        // the stages the search runs, across the picker: a box for each the daemon can, checked or
        // not; one of Lexical and Semantic always stays checked
        Flex {
            direction: Tui.Horizontal
            CheckBox { text: "Lexical"; checked: App.stageLexical; onToggled: App.toggleSearchStage("lexical") }
            Text { text: "  " }
            CheckBox { text: "Semantic"; checked: App.stageSemantic; visible: App.stageSemanticShown; onToggled: App.toggleSearchStage("semantic") }
            Text { text: "  "; visible: App.stageRerankShown }
            CheckBox { text: App.stageRerankText; checked: App.stageRerank; visible: App.stageRerankShown; onToggled: App.toggleSearchStage("rerank") }
        }
    }
    Shortcut { sequence: "Ctrl+K"; onActivated: query.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+J"; onActivated: hits.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+L"; onActivated: searchPreview.forceActiveFocus() }
    Shortcut { sequence: "Ctrl+H"; onActivated: hits.forceActiveFocus() }
    DialogButtonBox {
        Button { text: "&Open"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.openHit(hits.currentIndex)
    onRejected: App.searchClosed()
}
