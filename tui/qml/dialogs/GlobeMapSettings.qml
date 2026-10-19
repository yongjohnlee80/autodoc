// GlobeMapSettings.qml — Relations › GlobeMap settings… and Preferences › GlobeMap… (the GUI's): the
// GlobeMap's own settings (globesettings_gui.go). Each applies at once and the store keeps it; a
// colour field applies as it is typed, once it is #rrggbb or empty (the theme's colour).
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 90
    title: "GlobeMap settings"
    width: 90
    dim: false
    helpText: App.globeSettingsError
    Flex {
        direction: Tui.Horizontal
        Frame {
            title: "map"
            Layout.fillWidth: true
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
                    Text { text: "" }
                    Text { text: "colour rules, the first match wins:"; toolTipText: "#tag=#rrggbb colours a tag's documents (its nested tags too); folder/=#rrggbb a folder's. Apart by ;" }
                    TextField { id: globeRules; text: App.globeRulesText; placeholderText: "#tag=#rrggbb; folder/=#rrggbb"; onTextEdited: App.setGlobeRules(text); onAccepted: App.setGlobeRules(text) }
                }
                Text { text: " " }
            }
        }
        Text { text: " " }
        Frame {
            title: "colours (#rrggbb; empty: the theme's)"
            Layout.fillWidth: true
            Flex {
                direction: Tui.Horizontal
                Text { text: " " }
                Flex {
                    direction: Tui.Vertical
                    Layout.fillWidth: true
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "ADRs"; Layout.preferredWidth: 18 }
                        TextField { text: App.globeAdrColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("adr", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "reviews"; Layout.preferredWidth: 18 }
                        TextField { text: App.globeReviewColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("review", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "conventions"; Layout.preferredWidth: 18 }
                        TextField { text: App.globeConventionColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("convention", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "syntheses"; Layout.preferredWidth: 18 }
                        TextField { text: App.globeSynthesisColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("synthesis", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "PRs"; Layout.preferredWidth: 18 }
                        TextField { text: App.globePrColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("pr", text) }
                    }
                    Flex {
                        direction: Tui.Horizontal
                        Text { text: "other documents"; Layout.preferredWidth: 18 }
                        TextField { text: App.globeOtherColour; placeholderText: "the theme's"; onTextEdited: App.setGlobeColour("other", text) }
                    }
                    Text { text: "" }
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
