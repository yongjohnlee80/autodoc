Dialog {
    closeOnQ: false
    title: App.sectionTitle
    width: 52
    helpText: App.sectionError
    Flex {
        direction: Tui.Vertical
        Text { text: "section size (estimated tokens)" }
        TextField { id: sectionSize; text: App.sectionSize }
    }
    DialogButtonBox {
        Button { text: "&Save"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "&Cancel"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.saveSectionSize(sectionSize.text)
}
