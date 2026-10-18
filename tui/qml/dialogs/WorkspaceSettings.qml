// WorkspaceSettings.qml — a workspace's settings in one dialog, in two tabs (ADR 0214). Edit is what
// the workspace is and which files it holds; Advanced is how they are indexed and embedded, and,
// where the edition offers them, the databases: a source its .view files read, and the destination
// its index is kept in. Save sends every change at once, and a refusal changes nothing: the dialog
// opens again as it was typed, the reason on its help line. Adding a workspace uses it too, with
// the root typed, browsed to or made (New Folder), and a KB type to scaffold it with (ADR
// 1792349867). Each tab's frames stack top to bottom. Enter in a field saves, as Save does.
Dialog {
    closeOnQ: true
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
                direction: Tui.Vertical
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
                        Flex {
                            direction: Tui.Horizontal
                            visible: App.settingsAdding
                            Button { text: "Browse…"; onClicked: App.browseRoot(sRoot.text) }
                            Text { text: " " }
                            Button { text: "New Folder"; toolTipText: "create the folder the root names"; onClicked: App.newRootFolder(sRoot.text) }
                        }
                        Text { text: ""; visible: App.settingsAdding }
                        Text { text: "KB type: the folders and files to start it with"; visible: App.settingsAdding }
                        ComboBox { id: sKBType; visible: App.settingsAdding; model: App.kbTypes; textRole: "label"; currentIndex: App.kbTypeIndex; onActivated: App.kbTypePicked(index) }
                        Flex {
                            direction: Tui.Horizontal
                            visible: App.kbTypeCustomShown
                            TextField { id: sKBURL; Layout.fillWidth: true; text: "https://" }
                            Text { text: " " }
                            Button { text: "Stage"; onClicked: App.kbTypeURL(sKBURL.text) }
                        }
                        Text { text: App.kbTypeSummary; visible: App.settingsAdding; wrapMode: Tui.WordWrap }
                        Text { text: "" }
                        Text { text: "frontmatter schema (relative to the root, or absolute; blank for none)" }
                        TextField { id: sSchema; text: App.settingsSchema }
                        Text { text: App.settingsSchemaState; wrapMode: Tui.WordWrap }
                    }
                }
                Frame {
                    title: "files"
                    Layout.fillWidth: true
                    Flex {
                        direction: Tui.Vertical
                        Flex {
                            direction: Tui.Horizontal
                            Text { text: "Markdown (.md)      " }
                            ComboBox { id: sMd; model: App.yesNo; textRole: "label"; currentIndex: App.settingsMdIndex }
                        }
                        Flex {
                            direction: Tui.Horizontal
                            Text { text: "Plain text (.txt)   " }
                            ComboBox { id: sTxt; model: App.yesNo; textRole: "label"; currentIndex: App.settingsTxtIndex }
                        }
                        Flex {
                            direction: Tui.Horizontal
                            Text { text: "YAML (.yaml, .yml)  " }
                            ComboBox { id: sYaml; model: App.yesNo; textRole: "label"; currentIndex: App.settingsYamlIndex }
                        }
                        Text { text: "your text types (comma-separated: .log, .rst)" }
                        TextField { id: sTexts; text: App.settingsTexts }
                        Text { text: App.settingsTextsNote; visible: App.settingsTextsNoted; wrapMode: Tui.WordWrap }
                        Text { text: App.settingsCodeLabel; visible: App.settingsCodeOffered; wrapMode: Tui.WordWrap }
                        TextField { id: sCode; text: App.settingsCode; visible: App.settingsCodeOffered }
                        Text { text: App.settingsDocsLabel; visible: App.settingsDocsOffered; wrapMode: Tui.WordWrap }
                        TextField { id: sDocs; text: App.settingsDocs; visible: App.settingsDocsOffered }
                        Text { text: "include globs (semicolon-separated; blank matches no files)" }
                        TextField { id: sInclude; text: App.settingsInclude }
                        Text { text: "exclude globs (semicolon-separated)" }
                        TextField { id: sExclude; text: App.settingsExclude }
                        Text { text: App.settingsTodoNote; visible: App.settingsTodoNoted; wrapMode: Tui.WordWrap }
                        Button { text: "Index tasks graph-only instead"; visible: App.settingsTodoNoted; onClicked: App.todoGraphOnly(sExclude.text) }
                        Text { text: "graph-only globs (in links and relations, never in search)" }
                        TextField { id: sGraphOnly; text: App.settingsGraphOnly }
                        Flex {
                            direction: Tui.Horizontal
                            Text { text: "Todo tasks in search " }
                            ComboBox { id: sTodoSearch; model: App.yesNo; textRole: "label"; currentIndex: App.settingsTodoSearchIndex }
                        }
                    }
                }
            }
        }
        Tab {
            title: "Advanced"
            Flex {
                direction: Tui.Vertical
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
                        Text { text: "embedding provider (added in System › Search models)" }
                        ComboBox { id: sProvider; model: App.wsProviders; textRole: "label"; currentIndex: App.wsProviderIndex }
                        Text { text: App.settingsProviderState; wrapMode: Tui.WordWrap }
                    }
                }
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
    onRejected: App.settingsCancelled()
    onAccepted: App.saveSettings(sName.text, sRoot.text, sSchema.text, sTexts.text, sInclude.text, sExclude.text,
        sMd.currentIndex, sTxt.currentIndex, sYaml.currentIndex, sSection.text, sPolicy.currentIndex, sProvider.currentIndex,
        sDest.currentIndex, sDestDSN.text, sDestSchema.text, sIndex.currentIndex,
        sSource.currentIndex, sSrcDSN.text, sSrcSchema.text, sViewArgs.text, sCode.text, sDocs.text,
        sGraphOnly.text, sTodoSearch.currentIndex)
}
