// Vectors.qml — AI models › Vectors…: the workspace's models, each with the room its vectors take:
// the float32 vectors, the 1-bit codes and the rows' keys. Purge… removes a model no longer used.
Dialog {
    closeOnQ: true
    maxWidthPercent: 90
    maxHeightPercent: 80
    title: App.vectorsTitle
    dim: false
    Flex {
        direction: Tui.Vertical
        TableView {
            id: vectorTable
            palette.highlight: Theme.document.highlight
            palette.highlightedText: Theme.document.highlightedText
            model: App.vectors
            Layout.fillHeight: true
            TableViewColumn { role: "state"; title: "STATE"; width: 7 }
            TableViewColumn { role: "model"; title: "MODEL"; width: 0 }
            TableViewColumn { role: "dims"; title: "DIMS"; width: 5 }
            TableViewColumn { role: "vectors"; title: "VECTORS"; width: 8 }
            TableViewColumn { role: "f32"; title: "FLOAT32"; width: 10 }
            TableViewColumn { role: "bits"; title: "1-BIT"; width: 10 }
            TableViewColumn { role: "keys"; title: "KEYS"; width: 10 }
            TableViewColumn { role: "total"; title: "TOTAL"; width: 10 }
        }
		Text { text: App.vectorsStatus; wrapMode: Tui.WordWrap }
        Text { text: App.vectorsRefusals; wrapMode: Tui.WordWrap }
    }
    DialogButtonBox {
        Button { text: "&Purge…"; DialogButtonBox.buttonRole: DialogButtonBox.ActionRole; onClicked: App.startPurge(vectorTable.currentIndex) }
        Button { text: "Close (&q)"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
}
