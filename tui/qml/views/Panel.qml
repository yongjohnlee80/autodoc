// Panel.qml — the panels' one dock style: a Drawer over the page, framed under its heading, floating
// where it is dragged or arranged (panelfloat.go: Alt/Option-left drag moves it, Alt/Option-right
// drag resizes it) and until then at the edge its preference names. A use gives its id, its edge
// and geometry with the preferences they keep, its content, and the Shortcuts live while the
// keyboard is in the panel: the content and its keys go into the FocusScope together, and its keys
// win there over the Window's (tui/keys.go's scopes generate them).
Drawer {
    property string heading: ""
    // the frame's colour: the page's, as the lists in it are (a frame's own window tone would show
    // as a band beside its border, which the GUI draws as a thin line through the cell)
    property var surface: Theme.document.base
    default property alias content: scope.data
    modal: false
    movable: true
    windowResize: true
    Frame {
        title: heading
        palette.window: surface
        FocusScope { id: scope }
    }
}
