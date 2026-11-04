package tui

// A TERMINAL'S RELATION VIEWS, in either build: Relations › Mindmap drawn in cells (mindmap_cells.go),
// beside Relations › Links (relationmap.go). withRelationViews writes them into main.qml on a
// terminal; a window writes the window's (relationviews_gui.go). A terminal draws no GlobeMap.

// cellRelationMenuQML is the terminal's Relations menu item for its Mindmap.
const cellRelationMenuQML = `            MenuItem { text: qsTrId("autodoc.gui.relations.mindmap"); onTriggered: App.toggleMindmap() }
`

// cellRelationViewsQML is the terminal Mindmap's panel, a large float in the middle until the user
// moves or sizes it, as the window's is.
const cellRelationViewsQML = `    // the Mindmap in cells (mindmap_tui.go): the page's document and those a link or two from it
    Panel {
        id: mindmap
        heading: App.mindmapTitle
        closeOnQ: true
        edge: Tui.Center
        size: 85
        length: 90
        floating: App.mindmapFloating
        floatX: App.mindmapX; floatY: App.mindmapY; floatWidth: App.mindmapW; floatHeight: App.mindmapH
        onPlaced: App.panelPlaced("mindmap", x, y, width, height)
        minimumSize: 20
        onOpened: App.panelOpened("mindmap")
        onClosed: App.panelClosed("mindmap")
        CellMindmap {
            id: mindmapView
        }
    }
`
