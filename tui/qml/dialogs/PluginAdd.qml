// PluginAdd.qml — Plugins › Add from a git URL…: the repository holding the plugin. Cloning it
// runs nothing: PluginConfirm asks first, saying what would run, and at whose risk.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 60
    title: qsTrId("autodoc.dialog.pluginAdd.add_a_plugin")
    dim: true
    Flex {
        direction: Tui.Vertical
        Text { text: qsTrId("autodoc.dialog.pluginAdd.the_plugin_s_git_repository_or_its_name_in_the_r") }
        TextField {
            id: pluginUrl
            text: App.pluginUrl
            placeholderText: qsTrId("autodoc.dialog.pluginAdd.https_github_com_yongjohnlee80_autodoc_tetris")
            onAccepted: App.addPlugin(text)
        }
        Text { wrapMode: Tui.WordWrap; text: App.pluginRisk }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.pluginAdd.cancel"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: qsTrId("autodoc.dialog.pluginAdd.clone"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
    onAccepted: App.addPlugin(pluginUrl.text)
}
