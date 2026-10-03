Dialog {
    closeOnQ: true
    title: App.fileTypesTitle
    width: 72
    maxWidthPercent: 90
    dim: false
    helpText: App.fileTypesHelp
    Flex {
        direction: Tui.Vertical
        Text { text: App.fileTypesRoot }
        Text { text: App.fileTypesPatterns }
        Text { text: "" }
        Text { text: "Markdown (.md) · editable" }
        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.markdownTypeIndex; onActivated: App.setFileType(index, "md") }
        Text { text: "Plain text (.txt) · editable" }
        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.textTypeIndex; onActivated: App.setFileType(index, "txt") }
        Text { text: "YAML (.yaml, .yml) · editable" }
        ComboBox { model: App.yesNo; textRole: "label"; currentIndex: App.yamlTypeIndex; onActivated: App.setFileType(index, "yaml") }
        Text { text: "" }
        Text { text: "☐ Pro: .doc, .docx, .odt, .pdf · unavailable in Community" }
    }
    DialogButtonBox {
        Button { text: "&Rules…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.editActivePatterns() }
        Button { text: "&Close"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
