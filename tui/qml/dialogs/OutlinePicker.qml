// OutlinePicker.qml — Go › Outline (SPC O): the picker over the note's headings, as the editor's text
// has them, saved or not. On the left a filter, then the headings, indented by level; on the right
// the note at the heading under the cursor. Enter jumps there.
Picker {
    name: "outline"
    title: "outline"
    label: "filter: a part of a heading"
    fieldText: App.outlineField
    model: App.outlineRows
    listTitle: App.outlineStatus
    previewTitle: App.outlinePreviewTitle
    previewText: App.outlinePreviewText
    previewAt: App.outlinePreviewAt
    acceptText: "&Jump"
    TableViewColumn { role: "heading"; title: "HEADING"; width: 0 }
    TableViewColumn { role: "line"; title: "LINE"; width: 6 }
}
