// LuaPick.qml — a script's picker (autodoc.ui.pick, luahost.go): its items, filtered; on the right
// an item's detail. Choosing one answers the script; Esc answers it "cancelled".
Picker {
    name: "luaPick"
    title: App.luaPickTitle
    label: "filter"
    fieldText: App.luaPickField
    model: App.luaPickRows
    listTitle: App.luaPickStatus
    previewTitle: App.luaPickPreviewTitle
    previewText: App.luaPickPreviewText
    previewAt: App.luaPickPreviewAt
    acceptText: "&Choose"
    TableViewColumn { role: "label"; title: "ITEM"; width: 0 }
}
