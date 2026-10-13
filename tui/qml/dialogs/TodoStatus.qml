// TodoStatus.qml — the todos panel's s: every status a task can have, its own marked ●, and on the
// right what choosing one does. Choosing moves the task there (todos_status.go). Small: five rows.
Picker {
    name: "todoStatus"
    title: "status…"
    label: "filter: a part of a status"
    fieldText: App.todoStatusField
    model: App.todoStatusRows
    listTitle: App.todoStatusList
    previewTitle: App.todoStatusPreviewTitle
    previewText: App.todoStatusPreviewText
    previewAt: App.todoStatusPreviewAt
    acceptText: "&Choose"
    widthPercent: 60
    heightPercent: 50
    TableViewColumn { role: "status"; title: "STATUS"; width: 0 }
}
