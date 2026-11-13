// Compare.qml — the conflict's Compare (compare.go): the disk's text at the left, the buffer's at the
// right, the result between them. A terminal's only: written into main.qml when the program does not
// run as a window, golib's MergeView having no window's yet; never an autodoc.dialogs component,
// which every build parses.
Dialog {
    maxWidthPercent: 98
    maxHeightPercent: 95
    title: App.compareTitle
    onClosed: App.compareClosed()
    Flex {
        direction: Tui.Vertical
        MergeView {
            id: compareView
            Layout.fillHeight: true
            onClosed: App.compareDone(applied)
        }
    }
}
