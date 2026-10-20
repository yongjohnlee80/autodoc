// WorkspaceSettings.qml — a workspace's settings in one dialog, in two tabs (ADR 0214). Edit is what
// the workspace is and which files it holds; Advanced is how they are indexed and embedded, and,
// where the edition offers them, the databases: a source its .view files read, and the destination
// its index is kept in. Save sends every change at once, and a refusal changes nothing: the dialog
// opens again as it was typed, the reason on its help line. Adding a workspace uses it too, with
// the root typed, browsed to or made (New Folder), and a KB type to scaffold it with (ADR
// 1792349867). Enter in a field saves, as Save does. Each tab's frames are stacked, top to bottom,
// in a ScrollView: the wheel and the page keys scroll it, and a field taking focus is brought into view.
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
            ScrollView {
                Flex {
                    direction: Tui.Vertical
                    Frame {
                        title: qsTrId("autodoc.dialog.workspaceSettings.workspace")
                        Layout.fillWidth: true
                        Flex {
                            direction: Tui.Vertical
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.title") }
                            TextField { id: sName; text: App.settingsName }
                            Text { text: "" }
                            Text { text: App.settingsRootLine; wrapMode: Tui.WordWrap }
                            TextField { id: sRoot; text: App.settingsRoot; visible: App.settingsAdding }
                            Flex {
                                direction: Tui.Horizontal
                                visible: App.settingsAdding
                                Button { text: qsTrId("autodoc.dialog.workspaceSettings.browse"); onClicked: App.browseRoot(sRoot.text) }
                                Text { text: " " }
                                Button { text: qsTrId("autodoc.dialog.workspaceSettings.new_folder"); toolTipText: "create the folder the root names"; onClicked: App.newRootFolder(sRoot.text) }
                            }
                            Text { text: ""; visible: App.settingsAdding }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.kb_type_the_folders_and_files_to_start_it_with"); visible: App.settingsAdding }
                            ComboBox { id: sKBType; visible: App.settingsAdding; model: App.kbTypes; textRole: "label"; currentIndex: App.kbTypeIndex; onActivated: App.kbTypePicked(index) }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.the_kb_type_s_https_git_url"); visible: App.kbTypeCustomShown }
                            TextField { id: sKBURL; visible: App.kbTypeCustomShown; text: "https://" }
                            Button { text: qsTrId("autodoc.dialog.workspaceSettings.stage"); visible: App.kbTypeCustomShown; onClicked: App.kbTypeURL(sKBURL.text) }
                            Text { text: App.kbTypeSummary; visible: App.settingsAdding; wrapMode: Tui.WordWrap }
                            Text { text: "" }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.frontmatter_schema_relative_to_the_root_or_absol") }
                            TextField { id: sSchema; text: App.settingsSchema }
                            Text { text: App.settingsSchemaState; wrapMode: Tui.WordWrap }
                        }
                    }
                    Text { text: "" }
                    Frame {
                        title: qsTrId("autodoc.dialog.workspaceSettings.files")
                        Layout.fillWidth: true
                        Flex {
                            direction: Tui.Vertical
                            Flex {
                                direction: Tui.Horizontal
                                Text { text: qsTrId("autodoc.dialog.workspaceSettings.markdown_md") }
                                ComboBox { id: sMd; model: App.yesNo; textRole: "label"; currentIndex: App.settingsMdIndex }
                            }
                            Flex {
                                direction: Tui.Horizontal
                                Text { text: qsTrId("autodoc.dialog.workspaceSettings.plain_text_txt") }
                                ComboBox { id: sTxt; model: App.yesNo; textRole: "label"; currentIndex: App.settingsTxtIndex }
                            }
                            Flex {
                                direction: Tui.Horizontal
                                Text { text: qsTrId("autodoc.dialog.workspaceSettings.yaml_yaml_yml") }
                                ComboBox { id: sYaml; model: App.yesNo; textRole: "label"; currentIndex: App.settingsYamlIndex }
                            }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.your_text_types_comma_separated_log_rst") }
                            TextField { id: sTexts; text: App.settingsTexts }
                            Text { text: App.settingsTextsNote; visible: App.settingsTextsNoted; wrapMode: Tui.WordWrap }
                            Text { text: App.settingsCodeLabel; visible: App.settingsCodeOffered; wrapMode: Tui.WordWrap }
                            TextField { id: sCode; text: App.settingsCode; visible: App.settingsCodeOffered }
                            Text { text: App.settingsDocsLabel; visible: App.settingsDocsOffered; wrapMode: Tui.WordWrap }
                            TextField { id: sDocs; text: App.settingsDocs; visible: App.settingsDocsOffered }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.include_globs_semicolon_separated_blank_matches") }
                            TextField { id: sInclude; text: App.settingsInclude }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.exclude_globs_semicolon_separated") }
                            TextField { id: sExclude; text: App.settingsExclude }
                            Text { text: App.settingsTodoNote; visible: App.settingsTodoNoted; wrapMode: Tui.WordWrap }
                            Button { text: qsTrId("autodoc.dialog.workspaceSettings.index_tasks_graph_only_instead"); visible: App.settingsTodoNoted; onClicked: App.todoGraphOnly(sExclude.text) }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.graph_only_globs_in_links_and_relations_never_in") }
                            TextField { id: sGraphOnly; text: App.settingsGraphOnly }
                            Flex {
                                direction: Tui.Horizontal
                                Text { text: qsTrId("autodoc.dialog.workspaceSettings.todo_tasks_in_search") }
                                ComboBox { id: sTodoSearch; model: App.yesNo; textRole: "label"; currentIndex: App.settingsTodoSearchIndex }
                            }
                        }
                    }
                }
            }
        }
        Tab {
            title: "Advanced"
            ScrollView {
                Flex {
                    direction: Tui.Vertical
                    Frame {
                        title: qsTrId("autodoc.dialog.workspaceSettings.indexing")
                        Layout.fillWidth: true
                        Flex {
                            direction: Tui.Vertical
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.section_size_128_2048_estimated_tokens_a_change") }
                            TextField { id: sSection; text: App.settingsSection }
                            Text { text: "" }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.embedding") }
                            ComboBox { id: sPolicy; model: App.policies; textRole: "label"; currentIndex: App.settingsPolicyIndex }
                            Text { text: "" }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.embedding_provider_added_in_system_search_models") }
                            ComboBox { id: sProvider; model: App.wsProviders; textRole: "label"; currentIndex: App.wsProviderIndex }
                            Text { text: App.settingsProviderState; wrapMode: Tui.WordWrap }
                        }
                    }
                    Text { text: ""; visible: App.databasesShown }
                    Frame {
                        title: qsTrId("autodoc.dialog.workspaceSettings.source_database")
                        Layout.fillWidth: true
                        visible: App.databasesShown
                        Flex {
                            direction: Tui.Vertical
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.engine") }
                            ComboBox { id: sSource; model: App.sourceEngines; textRole: "label"; currentIndex: App.settingsSourceIndex }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.dsn_or_a_sqlite_file_s_path") }
                            TextField { id: sSrcDSN; text: App.settingsSrcDSN; echoMode: TextInput.Password }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.schema") }
                            TextField { id: sSrcSchema; text: App.settingsSrcSchema }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.view_args_key_value_key_value") }
                            TextField { id: sViewArgs; text: App.settingsViewArgs }
                            Text { text: App.settingsSourceState; wrapMode: Tui.WordWrap }
                        }
                    }
                    Text { text: ""; visible: App.databasesShown }
                    Frame {
                        title: qsTrId("autodoc.dialog.workspaceSettings.destination")
                        Layout.fillWidth: true
                        visible: App.databasesShown
                        Flex {
                            direction: Tui.Vertical
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.the_index_is_kept_in") }
                            ComboBox { id: sDest; model: App.destinations; textRole: "label"; currentIndex: App.settingsDestIndex }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.postgres_dsn") }
                            TextField { id: sDestDSN; text: App.settingsDestDSN; echoMode: TextInput.Password }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.schema") }
                            TextField { id: sDestSchema; text: App.settingsDestSchema }
                            Text { text: qsTrId("autodoc.dialog.workspaceSettings.vector_index") }
                            ComboBox { id: sIndex; model: App.vectorIndexes; textRole: "label"; currentIndex: App.settingsVectorIndex }
                            Text { text: App.settingsDestState; wrapMode: Tui.WordWrap }
                        }
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
