// RegistryBrowse.qml — Plugins › Browse the registry… (registrybrowse.go): the registry's plugins and
// Lua packages, each previewed with what it declares and its audits. Install asks first, as any
// install does.
Picker {
    name: "registry"
    title: qsTrId("autodoc.dialog.registryBrowse.the_registry_plugins_and_lua_packages")
    label: qsTrId("autodoc.dialog.registryBrowse.filter_a_name_a_title_or_words_of_a_description")
    fieldText: App.registryField
    model: App.registryRows
    listTitle: App.registryList
    note: App.registryNote
    noteShown: App.registryNoteShown
    previewTitle: App.registryPreviewTitle
    previewText: App.registryPreviewText
    previewAt: App.registryPreviewAt
    acceptText: qsTrId("autodoc.dialog.registryBrowse.install")
    TableViewColumn { role: "entry"; title: "ENTRY"; width: 0 }
}
