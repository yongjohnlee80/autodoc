// TodoVar.qml — the todos panel's Variables tab: e, m and a ask for a variable's value here (a:
// NAME=value); Enter keeps it, in the KB's file or for this machine as the title says, and an
// empty value clears it (todos_vars.go).
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    title: App.todoVarTitle
    width: 72
    dim: false
    Flex {
        direction: Tui.Vertical
        Text { text: qsTrId("autodoc.dialog.todoVar.value_a_relative_path_is_the_store_s_folder_s_em") }
        TextField { id: todoVarField; text: App.todoVarText; placeholderText: qsTrId("autodoc.dialog.todoVar.abs_path_relative_or_path"); onAccepted: App.todoVarSet(text) }
    }
}
