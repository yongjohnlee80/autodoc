// WorkspaceSettings.qml — a workspace's settings in one dialog, in two tabs (ADR 0214). Edit is what
// the workspace is and which files it holds; Advanced is how they are indexed and embedded, and,
// where the edition offers them, the databases: a source its .view files read, and the destination
// its index is kept in. Save sends every change at once, and a refusal changes nothing: the dialog
// opens again as it was typed, the reason on its help line. Adding a workspace uses it too, with
// the root typed or browsed to. Enter in a field saves, as Save does.
Dialog {
    closeOnQ: false
    standardButtons: Dialog.Save | Dialog.Cancel
    defaultButton: Dialog.Save
    maxWidthPercent: 95
    maxHeightPercent: 95
    title: App.settingsTitle
    width: 112
    dim: false
    helpText: App.settingsHelp
    TabView {
        id: settingsTabs
        currentIndex: App.settingsTab
        onCurrentIndexChanged: App.settingsTabMoved(index)
        Tab {
            title: "Edit"
            Flex {
                direction: Tui.Horizontal
                Frame {
                    title: "workspace"
                    Layout.fillWidth: true
                    Flex {
                        direction: Tui.Vertical
                        Text { text: "title" }
                        TextField { id: sName; text: App.settingsName }
                        Text { text: "" }
                        Text { text: App.settingsRootLine; wrapMode: Tui.WordWrap }
                        TextField { id: sRoot; text: App.settingsRoot; visible: App.settingsAdding }
                        Button { text: "Browse…"; visible: App.settingsAdding; onClicked: App.browseRoot(sRoot.text) }
                        Text { text: "" }
                        Text { text: "frontmatter schema (relative to the root, or absolute; blank for none)" }
                        TextField { id: sSchema; text: App.settingsSchema }
                        Text { text: App.settingsSchemaState; wrapMode: Tui.WordWrap }
                    }
                }
                Text { text: " " }
                Frame {
                    title: "files"
                    Layout.fillWidth: true
                    Flex {
                        direction: Tui.Vertical
                        Text { text: "Markdown (.md)" }
                        ComboBox { id: sMd; model: App.yesNo; textRole: "label"; currentIndex: App.settingsMdIndex }
                        Text { text: "Plain text (.txt)" }
                        ComboBox { id: sTxt; model: App.yesNo; textRole: "label"; currentIndex: App.settingsTxtIndex }
                        Text { text: "YAML (.yaml, .yml)" }
                        ComboBox { id: sYaml; model: App.yesNo; textRole: "label"; currentIndex: App.settingsYamlIndex }
                        Text { text: "your text types (comma-separated: .log, .rst)" }
                        TextField { id: sTexts; text: App.settingsTexts }
                        Text { text: "include globs (semicolon-separated; blank matches no files)" }
                        TextField { id: sInclude; text: App.settingsInclude }
                        Text { text: "exclude globs (semicolon-separated)" }
                        TextField { id: sExclude; text: App.settingsExclude }
                        Text { text: "☐ Pro: .doc, .docx, .odt, .pdf · unavailable in Community" }
                    }
                }
            }
        }
        Tab {
            title: "Advanced"
            Flex {
                direction: Tui.Horizontal
                Frame {
                    title: "indexing"
                    Layout.fillWidth: true
                    Flex {
                        direction: Tui.Vertical
                        Text { text: "section size (128–2048 estimated tokens; a change re-chunks)" }
                        TextField { id: sSection; text: App.settingsSection }
                        Text { text: "" }
                        Text { text: "embedding" }
                        ComboBox { id: sPolicy; model: App.policies; textRole: "label"; currentIndex: App.settingsPolicyIndex }
                        Text { text: "" }
                        Text { text: "embedding provider (added in System › AI models)" }
                        ComboBox { id: sProvider; model: App.wsProviders; textRole: "label"; currentIndex: App.wsProviderIndex }
                        Text { text: App.settingsProviderState; wrapMode: Tui.WordWrap }
                    }
                }
                Text { text: " "; visible: App.databasesShown }
                Frame {
                    title: "source database"
                    Layout.fillWidth: true
                    visible: App.databasesShown
                    Flex {
                        direction: Tui.Vertical
                        Text { text: "engine" }
                        ComboBox { id: sSource; model: App.sourceEngines; textRole: "label"; currentIndex: App.settingsSourceIndex }
                        Text { text: "DSN, or a sqlite file's path" }
                        TextField { id: sSrcDSN; text: App.settingsSrcDSN; echoMode: TextInput.Password }
                        Text { text: "schema" }
                        TextField { id: sSrcSchema; text: App.settingsSrcSchema }
                        Text { text: "view args (key=value; key=value)" }
                        TextField { id: sViewArgs; text: App.settingsViewArgs }
                        Text { text: App.settingsSourceState; wrapMode: Tui.WordWrap }
                    }
                }
                Text { text: " "; visible: App.databasesShown }
                Frame {
                    title: "destination"
                    Layout.fillWidth: true
                    visible: App.databasesShown
                    Flex {
                        direction: Tui.Vertical
                        Text { text: "the index is kept in" }
                        ComboBox { id: sDest; model: App.destinations; textRole: "label"; currentIndex: App.settingsDestIndex }
                        Text { text: "postgres DSN" }
                        TextField { id: sDestDSN; text: App.settingsDestDSN; echoMode: TextInput.Password }
                        Text { text: "schema" }
                        TextField { id: sDestSchema; text: App.settingsDestSchema }
                        Text { text: "vector index" }
                        ComboBox { id: sIndex; model: App.vectorIndexes; textRole: "label"; currentIndex: App.settingsVectorIndex }
                        Text { text: App.settingsDestState; wrapMode: Tui.WordWrap }
                    }
                }
            }
        }
    }
    onAccepted: App.saveSettings(sName.text, sRoot.text, sSchema.text, sTexts.text, sInclude.text, sExclude.text,
        sMd.currentIndex, sTxt.currentIndex, sYaml.currentIndex, sSection.text, sPolicy.currentIndex, sProvider.currentIndex,
        sDest.currentIndex, sDestDSN.text, sDestSchema.text, sIndex.currentIndex,
        sSource.currentIndex, sSrcDSN.text, sSrcSchema.text, sViewArgs.text)
}
