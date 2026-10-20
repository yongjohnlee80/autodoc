// RankerEdit.qml — a ranker, added or edited: its name, its kind (which fills in where it usually
// is), its base URL, its model for a rerank API (a TEI server serves one), and its API key. Check
// asks the ranker what it serves. A key is sealed in the store and never shown again. A refusal
// opens it again with the reason on its help line: an edit of the ranker in use is set up and
// probed before it is saved.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 90
    title: App.rankerFormTitle
    width: 84
    dim: false
    helpText: App.rankerFormError
    Flex {
        direction: Tui.Vertical
        Text { text: qsTrId("autodoc.dialog.rankerEdit.name") }
        TextField { id: rName; text: App.rankerName; placeholderText: qsTrId("autodoc.dialog.rankerEdit.local_cohere") }
        Text { text: qsTrId("autodoc.dialog.rankerEdit.kind") }
        ComboBox { model: App.rankerKinds; textRole: "label"; currentIndex: App.rankerKindIndex; onActivated: App.rankerKindChosen(index, rBase.text) }
        Text { text: qsTrId("autodoc.dialog.rankerEdit.base_url") }
        TextField { id: rBase; text: App.rankerBase }
        Text { text: qsTrId("autodoc.dialog.rankerEdit.model"); visible: App.rankerModelShown }
        TextField { id: rModel; text: App.rankerModel; visible: App.rankerModelShown; placeholderText: qsTrId("autodoc.dialog.rankerEdit.a_re_ranking_model_rerank_v3_5_say") }
        Text { text: App.rankerKeyLabel }
        TextField { id: rKey; text: App.rankerKey; echoMode: TextInput.Password }
        Text { text: App.rankerModelsStatus; wrapMode: Tui.WordWrap }
    }
    DialogButtonBox {
        Button { text: qsTrId("autodoc.dialog.rankerEdit.check"); DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.checkRanker(rBase.text, rKey.text) }
        Button { text: qsTrId("autodoc.dialog.rankerEdit.save"); DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: qsTrId("autodoc.dialog.rankerEdit.close_q"); DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.saveRanker(rName.text, rBase.text, rModel.text, rKey.text)
}
