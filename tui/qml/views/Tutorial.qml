// Tutorial.qml — Help › Tutorial… (SPC T): a page at a time (tutorial.go), Markdown shown Rendered
// in the GUI's window (a terminal's Editor stays Raw, as the source reads), read-only, with Previous
// and Next (not B for Back: the page's Editor takes b, a word back). At most 80% of the window's
// height. Esc closes it.
Dialog {
    id: tutorial
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
        Button { text: "P&revious"; enabled: App.tutorialBack; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.tutorialPage("back") }
        Button { text: "&Next"; enabled: App.tutorialNext; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.tutorialPage("next") }
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
