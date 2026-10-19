// GlobeMapSettings.qml — Relations › GlobeMap settings… and Preferences › GlobeMap…: the GlobeMap's own
// settings (globesettings_gui.go). The GUI's alone: written into main.qml whole (relationviews_gui.go),
// not an autodoc.dialogs component, which a terminal's document could name. Each applies at once and
// the store keeps it; a colour field applies as it is typed, once it is #rrggbb or empty (the theme's).
Dialog {
    closeOnQ: true
    maxWidthPercent: 96
    maxHeightPercent: 90
    title: "GlobeMap settings"
    width: 140
    dim: false
    helpText: App.globeSettingsError
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
                        Text { text: "theme"; Layout.preferredWidth: 18; toolTipText: "the terminals' theme (the default), the page's, or one of its own" }
                        ComboBox { model: App.globeThemes; textRole: "label"; currentIndex: App.globeThemeIndex; onActivated: App.setGlobeTheme(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "names"; Layout.preferredWidth: 18; toolTipText: "the documents' names, once few enough are in view" }
                        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.globeNamesIndex; onActivated: App.setGlobeNames(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "opens with"; Layout.preferredWidth: 18; toolTipText: "the links it follows when it opens; f cycles them while it is open" }
                        ComboBox { model: App.globeFilters; textRole: "label"; currentIndex: App.globeFilterIndex; onActivated: App.setGlobeFilter(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "dot size"; Layout.preferredWidth: 18 }
                        ComboBox { model: App.globeDotSizes; textRole: "label"; currentIndex: App.globeDotsIndex; onActivated: App.setGlobeDots(index) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "settling speed"; Layout.preferredWidth: 18; toolTipText: "how fast the map settles: slow 1, gentle 2, normal 4, fast 8 steps a frame" }
                        ComboBox { model: App.globeSpeeds; textRole: "label"; currentIndex: App.globeSpeedIndex; onActivated: App.setGlobeSpeed(index) }
                    }
                }
                Text { text: " " }
            }
        }
        Text { text: " " }
        Frame {
            title: "groups: the first a document matches is its colour"
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
                        Text { text: "query"; Layout.preferredWidth: 10; toolTipText: "type:<type> (the schema's), tag:#<tag> (a nested tag too), path:<folder>/" }
                        TextField { text: App.globeGroupQuery; onTextEdited: App.globeGroupQuerySet(text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "colour"; Layout.preferredWidth: 10 }
                        TextField { text: App.globeGroupColour; placeholderText: "#rrggbb"; onTextEdited: App.globeGroupColourSet(text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Button { text: "&Add"; onClicked: App.globeGroupAdd() }
                        Button { text: "Remove"; onClicked: App.globeGroupRemove() }
                        Button { text: "Up"; toolTipText: "earlier groups win"; onClicked: App.globeGroupUp() }
                        Button { text: "Down"; onClicked: App.globeGroupDown() }
                        Button { text: "Add missing"; toolTipText: "a group for each schema type and folder no group queries"; onClicked: App.globeGroupsAddMissing() }
                    }
                    Text { text: "" }
                    Text { text: "other colours (#rrggbb; empty: the theme's)" }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "no group's"; Layout.preferredWidth: 18 }
                        TextField { text: App.globeOtherColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("other", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "body links"; Layout.preferredWidth: 18; toolTipText: "wikilinks, embeds and Markdown links" }
                        TextField { text: App.globeBodyLinkColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("link.body", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "supersession links"; Layout.preferredWidth: 18 }
                        TextField { text: App.globeSupersessionLinkColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("link.supersession", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "other relations"; Layout.preferredWidth: 18; toolTipText: "amends, related, sources, adr, review, blocked" }
                        TextField { text: App.globeRelationLinkColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("link.relation", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "superseded ring"; Layout.preferredWidth: 18 }
                        TextField { text: App.globeRingColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("superseded", text) }
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
