// Tutorial.qml — Help › Tutorial…: a page at a time (tutorial.go), Markdown shown Rendered
// in the GUI's window (a terminal's Editor stays Raw, as the source reads), read-only, with Previous
// and Next (not B for Back: the page's Editor takes b, a word back). Neither is ever disabled: on the
// first page Previous does nothing, and on the last Next is Close. At most 80% of the window's
// height. q or Esc closes it.
Dialog {
    id: tutorial
    closeOnQ: true
    title: App.tutorialTitle
    width: 84
    maxHeightPercent: 80
    dim: false
    Flex {
        direction: Tui.Vertical
        Editor {
            id: tutorialPage
            Layout.fillHeight: true
            readOnly: true
            wrap: true
            view: Editor.Rendered
            text: App.tutorialText
            SyntaxHighlighter { definition: "Markdown" }
            MarkdownRenderer {}
        }
    }
    DialogButtonBox {
        Button { text: "P&revious"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.tutorialPage("back") }
        Button { text: App.tutorialNextText; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.tutorialPage("next") }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
