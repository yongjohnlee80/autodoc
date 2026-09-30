// PluginAdd.qml — Plugins › Add from a git URL…: the repository holding the plugin. Cloning it
// runs nothing: PluginConfirm asks first, saying what would run, and at whose risk.
Dialog {
    closeOnQ: false
    maxWidthPercent: 80
    maxHeightPercent: 60
    title: "add a plugin"
    dim: true
    Flex {
        direction: Tui.Vertical
        Text { text: "the plugin's git repository" }
        TextField {
            id: pluginUrl
            text: App.pluginUrl
            placeholderText: "https://github.com/owner/autodoc-tetris"
            onAccepted: App.addPlugin(text)
        }
        Text { wrapMode: Tui.WordWrap; text: App.pluginRisk }
    }
    DialogButtonBox {
        Button { text: "&Cancel"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
        Button { text: "C&lone"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
    }
    onAccepted: App.addPlugin(pluginUrl.text)
}
