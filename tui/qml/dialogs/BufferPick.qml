// BufferPick.qml — Go › Switch buffer… (and :b with no argument): the picker over the open buffers.
// On the left a filter, then the buffers by number and name, [+] on a modified one; on the right the
// buffer under the cursor, its text as it is now. Enter shows it.
Picker {
    name: "buffers"
    title: "switch buffer"
    label: "filter: a part of the name or the number"
    fieldText: App.buffersField
    model: App.bufferRows
    listTitle: App.pickerStatus
    previewTitle: App.buffersPreviewTitle
    previewText: App.buffersPreviewText
    previewAt: App.buffersPreviewAt
    acceptText: "&Show"
    TableViewColumn { role: "label"; title: "BUFFER"; width: 0; elideMode: Tui.ElidePath }
}
