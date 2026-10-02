Dialog {
    closeOnQ: false
    title: App.policyTitle
    width: 56
    helpText: App.policyError
    Flex {
        direction: Tui.Vertical
        Text { text: "embedding policy" }
        TextField { id: policy; text: App.embeddingPolicy }
    }
    DialogButtonBox {
        Button { text: "&Save"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "&Cancel"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.saveEmbeddingPolicy(policy.text)
}
