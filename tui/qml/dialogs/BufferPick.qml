// BufferPick.qml — Go › Switch buffer… (and :b with no argument): the picker over the open buffers.
// On the left a filter, then the buffers by number and name, [+] on a modified one; on the right the
// buffer under the cursor, its text as it is now. Enter shows it.
Picker {
    name: "buffers"
    title: qsTrId("autodoc.dialog.bufferPick.switch_buffer")
    label: qsTrId("autodoc.dialog.bufferPick.filter_a_part_of_the_name_or_the_number")
    fieldText: App.buffersField
    model: App.bufferRows
    listTitle: App.pickerStatus
    previewTitle: App.buffersPreviewTitle
    previewText: App.buffersPreviewText
    previewAt: App.buffersPreviewAt
    acceptText: qsTrId("autodoc.dialog.bufferPick.show")
    TableViewColumn { role: "label"; title: "BUFFER"; width: 0; elideMode: Tui.ElidePath }
}
