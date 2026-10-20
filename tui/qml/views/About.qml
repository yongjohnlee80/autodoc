// About.qml — the version and its date; where the config, the daemon's log, the plugins and the
// daemon's socket are; the author and the licence. Each label is bold, its value beside it; the
// log is under the config, in brackets and italic. A program that replaces About's text
// (LaunchOptions.About) shows that instead.
Dialog {
    closeOnQ: true
    maxWidthPercent: 80
    maxHeightPercent: 80
    title: qsTrId("autodoc.view.about.about_autodoc")
    dim: true
    standardButtons: Dialog.Close
    defaultButton: Dialog.Close
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
}
