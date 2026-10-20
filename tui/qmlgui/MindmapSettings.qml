// MindmapSettings.qml — Relations › Mindmap settings…, as the GlobeMap's: the Mindmap's theme and the
// depth it opens at, and its groups, each a query, a colour and an outline (mindsettings_gui.go).
// The GUI's alone: written into main.qml whole (relationviews_gui.go), not an autodoc.dialogs
// component. Each applies at once; the groups are kept in the workspace's .autodoc/graph.toml.
Dialog {
    closeOnQ: true
    maxWidthPercent: 96
    maxHeightPercent: 90
    title: qsTrId("autodoc.gui.mindmapSettings.mindmap_settings")
    width: 140
    dim: false
    Flex {
        direction: Tui.Horizontal
        Frame {
            title: qsTrId("autodoc.gui.mindmapSettings.map")
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
                        Text { text: qsTrId("autodoc.gui.mindmapSettings.theme"); Layout.preferredWidth: 14; toolTipText: "the page's (the default), the terminals', or one of its own" }
                        ComboBox { model: App.mindThemes; textRole: "label"; currentIndex: App.mindThemeIndex; onActivated: App.setMindTheme(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.mindmapSettings.opens_at"); Layout.preferredWidth: 14; toolTipText: "how far from the page's document it reaches when it opens; d toggles it while it is open" }
                        ComboBox { model: App.mindDepths; textRole: "label"; currentIndex: App.mindDepthIndex; onActivated: App.setMindDepth(index) }
                    }
                }
                Text { text: " " }
            }
        }
        Text { text: " " }
        Frame {
            title: qsTrId("autodoc.gui.mindmapSettings.groups_the_first_a_document_matches_is_its_colou")
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
                        Text { text: qsTrId("autodoc.gui.mindmapSettings.query"); Layout.preferredWidth: 10; toolTipText: "type:<type> (the schema's), tag:#<tag> (a nested tag too), path:<folder>/" }
                        TextField { text: App.mindGroupQuery; onTextEdited: App.mindGroupQuerySet(text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.mindmapSettings.colour"); Layout.preferredWidth: 10 }
                        TextField { text: App.mindGroupColour; placeholderText: qsTrId("autodoc.gui.mindmapSettings.rrggbb"); onTextEdited: App.mindGroupColourSet(text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.mindmapSettings.outline"); Layout.preferredWidth: 10 }
                        ComboBox { model: App.mindShapeChoices; textRole: "label"; currentIndex: App.mindGroupShape; onActivated: App.mindGroupShapeSet(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Button { text: qsTrId("autodoc.gui.mindmapSettings.add"); onClicked: App.mindGroupAdd() }
                        Button { text: qsTrId("autodoc.gui.mindmapSettings.remove"); onClicked: App.mindGroupRemove() }
                        Button { text: qsTrId("autodoc.gui.mindmapSettings.up"); toolTipText: "earlier groups win"; onClicked: App.mindGroupUp() }
                        Button { text: qsTrId("autodoc.gui.mindmapSettings.down"); onClicked: App.mindGroupDown() }
                        Button { text: qsTrId("autodoc.gui.mindmapSettings.add_missing"); toolTipText: "a group for each schema type and folder no group queries"; onClicked: App.mindGroupsAddMissing() }
                    }
                }
                Text { text: " " }
            }
        }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.gui.mindmapSettings.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
