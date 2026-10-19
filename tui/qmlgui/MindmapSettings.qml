// MindmapSettings.qml — Relations › Mindmap settings…, as the GlobeMap's: the Mindmap's theme and the
// depth it opens at, and its groups, each a query, a colour and an outline (mindsettings_gui.go).
// The GUI's alone: written into main.qml whole (relationviews_gui.go), not an autodoc.dialogs
// component. Each applies at once; the groups are kept in the workspace's .autodoc/graph.toml.
Dialog {
    closeOnQ: true
    maxWidthPercent: 96
    maxHeightPercent: 90
    title: "Mindmap settings"
    width: 140
    dim: false
    Flex {
        direction: Tui.Horizontal
        Frame {
            title: "map"
            // a fixed width: the groups take the rest
            Layout.preferredWidth: 44
            Flex {
                direction: Tui.Horizontal
                Text { text: " " }
                Flex {
                    direction: Tui.Vertical
                    Layout.fillWidth: true
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "theme"; Layout.preferredWidth: 14; toolTipText: "the page's (the default), the terminals', or one of its own" }
                        ComboBox { model: App.mindThemes; textRole: "label"; currentIndex: App.mindThemeIndex; onActivated: App.setMindTheme(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "opens at"; Layout.preferredWidth: 14; toolTipText: "how far from the page's document it reaches when it opens; d toggles it while it is open" }
                        ComboBox { model: App.mindDepths; textRole: "label"; currentIndex: App.mindDepthIndex; onActivated: App.setMindDepth(index) }
                    }
                }
                Text { text: " " }
            }
        }
        Text { text: " " }
        Frame {
            title: "groups: the first a document matches is its colour and outline"
            Layout.fillWidth: true
            Flex {
                direction: Tui.Horizontal
                Text { text: " " }
                Flex {
                    direction: Tui.Vertical
                    Layout.fillWidth: true
                    Text { text: App.mindGroupsNote; wrapMode: Tui.WordWrap }
                    ListView {
                        id: mindGroups
                        palette.highlight: Theme.document.highlight
                        palette.highlightedText: Theme.document.highlightedText
                        model: App.mindGroupRows
                        textRole: "label"
                        currentIndex: App.mindGroupRow
                        onCurrentIndexChanged: App.mindGroupMoved(index)
                        Layout.fillHeight: true
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "query"; Layout.preferredWidth: 10; toolTipText: "type:<type> (the schema's), tag:#<tag> (a nested tag too), path:<folder>/" }
                        TextField { text: App.mindGroupQuery; onTextEdited: App.mindGroupQuerySet(text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "colour"; Layout.preferredWidth: 10 }
                        TextField { text: App.mindGroupColour; placeholderText: "#rrggbb"; onTextEdited: App.mindGroupColourSet(text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "outline"; Layout.preferredWidth: 10 }
                        ComboBox { model: App.mindShapeChoices; textRole: "label"; currentIndex: App.mindGroupShape; onActivated: App.mindGroupShapeSet(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Button { text: "&Add"; onClicked: App.mindGroupAdd() }
                        Button { text: "Remove"; onClicked: App.mindGroupRemove() }
                        Button { text: "Up"; toolTipText: "earlier groups win"; onClicked: App.mindGroupUp() }
                        Button { text: "Down"; onClicked: App.mindGroupDown() }
                        Button { text: "Add missing"; toolTipText: "a group for each schema type and folder no group queries"; onClicked: App.mindGroupsAddMissing() }
                    }
                }
                Text { text: " " }
            }
        }
    }
    DialogButtonBox {
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
