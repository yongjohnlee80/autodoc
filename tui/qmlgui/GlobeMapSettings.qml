// GlobeMapSettings.qml — Relations › GlobeMap settings… and Preferences › GlobeMap…: the GlobeMap's own
// settings (globesettings_gui.go). The GUI's alone: written into main.qml whole (relationviews_gui.go),
// not an autodoc.dialogs component, which a terminal's document could name. Each applies at once and
// the store keeps it; a colour field applies as it is typed, once it is #rrggbb or empty (the theme's).
Dialog {
    closeOnQ: true
    maxWidthPercent: 96
    maxHeightPercent: 90
    title: qsTrId("autodoc.gui.globeMapSettings.globemap_settings")
    width: 140
    dim: false
    helpText: App.globeSettingsError
    Flex {
        direction: Tui.Horizontal
        Frame {
            title: qsTrId("autodoc.gui.globeMapSettings.map")
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
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.theme"); Layout.preferredWidth: 18; toolTipText: "the terminals' theme (the default), the page's, or one of its own" }
                        ComboBox { model: App.globeThemes; textRole: "label"; currentIndex: App.globeThemeIndex; onActivated: App.setGlobeTheme(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.names"); Layout.preferredWidth: 18; toolTipText: "the documents' names, once few enough are in view" }
                        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.globeNamesIndex; onActivated: App.setGlobeNames(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.opens_with"); Layout.preferredWidth: 18; toolTipText: "the links it follows when it opens; f cycles them while it is open" }
                        ComboBox { model: App.globeFilters; textRole: "label"; currentIndex: App.globeFilterIndex; onActivated: App.setGlobeFilter(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.dot_size"); Layout.preferredWidth: 18 }
                        ComboBox { model: App.globeDotSizes; textRole: "label"; currentIndex: App.globeDotsIndex; onActivated: App.setGlobeDots(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.settling_speed"); Layout.preferredWidth: 18; toolTipText: "how fast the map settles: slow 1, gentle 2, normal 4, fast 8 steps a frame" }
                        ComboBox { model: App.globeSpeeds; textRole: "label"; currentIndex: App.globeSpeedIndex; onActivated: App.setGlobeSpeed(index) }
                    }
                }
                Text { text: " " }
            }
        }
        Text { text: " " }
        Frame {
            title: qsTrId("autodoc.gui.globeMapSettings.groups_the_first_a_document_matches_is_its_colou")
            Layout.fillWidth: true
            Flex {
                direction: Tui.Horizontal
                Text { text: " " }
                Flex {
                    direction: Tui.Vertical
                    Layout.fillWidth: true
                    Text { text: App.globeGroupsNote; wrapMode: Tui.WordWrap }
                    ListView {
                        id: globeGroups
                        palette.highlight: Theme.document.highlight
                        palette.highlightedText: Theme.document.highlightedText
                        model: App.globeGroupRows
                        textRole: "label"
                        currentIndex: App.globeGroupRow
                        onCurrentIndexChanged: App.globeGroupMoved(index)
                        Layout.fillHeight: true
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.query"); Layout.preferredWidth: 10; toolTipText: "type:<type> (the schema's), tag:#<tag> (a nested tag too), path:<folder>/" }
                        TextField { text: App.globeGroupQuery; onTextEdited: App.globeGroupQuerySet(text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.colour"); Layout.preferredWidth: 10 }
                        TextField { text: App.globeGroupColour; placeholderText: qsTrId("autodoc.gui.globeMapSettings.rrggbb"); onTextEdited: App.globeGroupColourSet(text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Button { text: qsTrId("autodoc.gui.globeMapSettings.add"); onClicked: App.globeGroupAdd() }
                        Button { text: qsTrId("autodoc.gui.globeMapSettings.remove"); onClicked: App.globeGroupRemove() }
                        Button { text: qsTrId("autodoc.gui.globeMapSettings.up"); toolTipText: "earlier groups win"; onClicked: App.globeGroupUp() }
                        Button { text: qsTrId("autodoc.gui.globeMapSettings.down"); onClicked: App.globeGroupDown() }
                        Button { text: qsTrId("autodoc.gui.globeMapSettings.add_missing"); toolTipText: "a group for each schema type and folder no group queries"; onClicked: App.globeGroupsAddMissing() }
                    }
                    Text { text: "" }
                    Text { text: qsTrId("autodoc.gui.globeMapSettings.other_colours_rrggbb_empty_the_theme_s") }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.no_group_s"); Layout.preferredWidth: 18 }
                        TextField { text: App.globeOtherColour; placeholderText: qsTrId("autodoc.gui.globeMapSettings.the_theme_s"); onTextEdited: App.setGlobeColour("other", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.body_links"); Layout.preferredWidth: 18; toolTipText: "wikilinks, embeds and Markdown links" }
                        TextField { text: App.globeBodyLinkColour; placeholderText: qsTrId("autodoc.gui.globeMapSettings.the_theme_s"); onTextEdited: App.setGlobeColour("link.body", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.supersession_links"); Layout.preferredWidth: 18 }
                        TextField { text: App.globeSupersessionLinkColour; placeholderText: qsTrId("autodoc.gui.globeMapSettings.the_theme_s"); onTextEdited: App.setGlobeColour("link.supersession", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.other_relations"); Layout.preferredWidth: 18; toolTipText: "amends, related, sources, adr, review, blocked" }
                        TextField { text: App.globeRelationLinkColour; placeholderText: qsTrId("autodoc.gui.globeMapSettings.the_theme_s"); onTextEdited: App.setGlobeColour("link.relation", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: qsTrId("autodoc.gui.globeMapSettings.superseded_ring"); Layout.preferredWidth: 18 }
                        TextField { text: App.globeRingColour; placeholderText: qsTrId("autodoc.gui.globeMapSettings.the_theme_s"); onTextEdited: App.setGlobeColour("superseded", text) }
                    }
                }
                Text { text: " " }
            }
        }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.gui.globeMapSettings.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
