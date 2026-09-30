// PluginManager.qml — Plugins › Manage plugins…: the plugins folder, each plugin's source and
// commit (a git clone's), or "local" for one put there by hand, and where its dialog sits. Add… one;
// Place moves the one under the cursor to the next placement it is designed for; Update… or
// Remove… it. The dialog stays open while each is asked for.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: "plugins"
    dim: false
    helpText: App.pluginsHelp
    TableView {
        id: pluginTable
        palette.highlight: Theme.document.highlight
        palette.highlightedText: Theme.document.highlightedText
        model: App.managedPlugins
        TableViewColumn { role: "name"; title: "PLUGIN"; width: 24 }
        TableViewColumn { role: "place"; title: "PLACE"; width: 7 }
        TableViewColumn { role: "commit"; title: "COMMIT"; width: 9 }
        TableViewColumn { role: "source"; title: "SOURCE"; width: 0; elideMode: Tui.ElidePath }
    }
    DialogButtonBox {
        Button { text: "&Add…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startAddPlugin() }
        Button { text: "&Place"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.placePlugin(pluginTable.currentIndex) }
        Button { text: "&Update…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startUpdatePlugin(pluginTable.currentIndex) }
        Button { text: "&Remove…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startRemovePlugin(pluginTable.currentIndex) }
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
