// RegistryBrowse.qml — Plugins › Browse the registry… (registrybrowse.go): the registry's plugins and
// Lua packages, each previewed with what it declares and its audits. Install asks first, as any
// install does.
Picker {
    name: "registry"
    title: "the registry: plugins and Lua packages"
    label: "filter: a name, a title or words of a description"
    fieldText: App.registryField
    model: App.registryRows
    listTitle: App.registryList
    note: App.registryNote
    noteShown: App.registryNoteShown
    previewTitle: App.registryPreviewTitle
    previewText: App.registryPreviewText
    previewAt: App.registryPreviewAt
    acceptText: "&Install"
    TableViewColumn { role: "entry"; title: "ENTRY"; width: 0 }
}
