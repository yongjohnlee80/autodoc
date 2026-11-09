// About.qml — the version and its date; where the config, the daemon's log, the plugins and the
// daemon's socket are; the author and the licence. Each label is bold, its value beside it; the
// log is under the config, in brackets and italic. A program that replaces About's text
// (LaunchOptions.About) shows that instead. Update shows when install-autodoc.sh installed this
// AutoDoc (its record says so): the update script runs in the terminal panel (update.go). It hides while
// the start's look for a newer release is out, and when that found this is the latest (startup.go).
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.view.about.about_autodoc")
    dim: true
    Flex {
        Text { text: App.aboutCustom ? App.aboutText : App.aboutHeadline; wrapMode: Tui.WordWrap }
        Text { text: "" }
        Flex { direction: Tui.Horizontal; Text { text: App.aboutCustom ? "" : qsTrId("autodoc.view.about.config"); font.bold: true } Text { text: App.aboutCustom ? "" : App.aboutConfig } }
        Text { text: App.aboutCustom ? "" : App.aboutLog; font.italic: true }
        Flex { direction: Tui.Horizontal; Text { text: App.aboutCustom ? "" : qsTrId("autodoc.view.about.plugins"); font.bold: true } Text { text: App.aboutCustom ? "" : App.aboutPlugins } }
        Flex { direction: Tui.Horizontal; Text { text: App.aboutCustom ? "" : qsTrId("autodoc.view.about.daemon_socket"); font.bold: true } Text { text: App.aboutCustom ? "" : App.aboutSocket } }
        Text { text: "" }
        Flex { direction: Tui.Horizontal; Text { text: App.aboutCustom ? "" : qsTrId("autodoc.view.about.author"); font.bold: true } Text { text: App.aboutCustom ? "" : App.aboutAuthor } }
        Text { text: App.aboutCustom ? "" : App.aboutLicence }
        Text { text: App.aboutUpdateShown ? " " : "" }
        Flex { direction: Tui.Horizontal; Text { text: App.aboutUpdateShown ? qsTrId("autodoc.view.about.update") : ""; font.bold: true } Text { text: App.aboutUpdate; wrapMode: Tui.WordWrap } }
        Text { text: App.aboutError; wrapMode: Tui.WordWrap; font.italic: true }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.view.about.update_now"); visible: App.aboutCanUpdate; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.updateAutoDoc() }
        Button { text: qsTrId("autodoc.view.about.close"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
