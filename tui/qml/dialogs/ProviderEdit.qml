// ProviderEdit.qml — an embedding provider, added or edited: its name, its kind (which fills in
// where it usually is), its base URL, its model — one the provider lists, or typed — its context
// window, for an Ollama kind, and its API key, for a kind that takes one. A key is sealed in the store and never shown again. A refusal
// opens it again with the reason on its help line.
Dialog {
    closeOnQ: true
    // inside Preferences, which it opens over: smaller, so its border is not read as that one's
    maxWidthPercent: 95
    maxHeightPercent: 95
    title: App.providerFormTitle
    width: 116
    dim: false
    helpText: App.providerFormError
    Split {
        orientation: Tui.Horizontal
        ratio: 0.6
        Flex {
            direction: Tui.Vertical
            Text { text: qsTrId("autodoc.dialog.providerEdit.name") }
            TextField { id: pName; text: App.providerName; placeholderText: qsTrId("autodoc.dialog.providerEdit.local_work") }
            Text { text: qsTrId("autodoc.dialog.providerEdit.kind") }
            ComboBox { model: App.providerKinds; textRole: "label"; currentIndex: App.providerKindIndex; onActivated: App.providerKindChosen(index, pBase.text) }
            Text { text: qsTrId("autodoc.dialog.providerEdit.base_url") }
            TextField { id: pBase; text: App.providerBase }
            Text { text: qsTrId("autodoc.dialog.providerEdit.model") }
            TextField { id: pModel; text: App.providerModel; placeholderText: qsTrId("autodoc.dialog.providerEdit.an_embedding_model_nomic_embed_text_say") }
            Text { text: qsTrId("autodoc.dialog.providerEdit.context_window"); visible: App.providerContextShown }
            TextField { id: pContext; text: App.providerContext; visible: App.providerContextShown }
            Text { text: App.providerContextHint; wrapMode: Tui.WordWrap; visible: App.providerContextShown }
            Text { text: qsTrId("autodoc.dialog.providerEdit.section_size") }
            Text { text: App.providerSectionHint; wrapMode: Tui.WordWrap }
            Text { text: App.providerKeyLabel; visible: App.providerKeyShown }
            TextField { id: pKey; text: App.providerKey; echoMode: TextInput.Password; visible: App.providerKeyShown }
        }
        Frame {
            title: qsTrId("autodoc.dialog.providerEdit.the_provider_s_models")
            Flex {
                direction: Tui.Vertical
                Text { text: App.providerModelsStatus; wrapMode: Tui.WordWrap }
                ListView {
                    id: models
                    palette.highlight: Theme.document.highlight
                    palette.highlightedText: Theme.document.highlightedText
                    model: App.providerModels
                    textRole: "name"
                    Layout.fillHeight: true
                    onActivated: App.pickProviderModel(index)
                }
            }
        }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.providerEdit.list_models"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.listProviderModels(pBase.text, pKey.text) }
        Button { text: qsTrId("autodoc.dialog.providerEdit.save"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: qsTrId("autodoc.dialog.providerEdit.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.saveProvider(pName.text, pBase.text, pModel.text, pKey.text, pContext.text)
}
