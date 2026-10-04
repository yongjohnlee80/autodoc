// WorkspaceProvider.qml — the workspace's embedding provider: the daemon's, or a stored one of its
// own (ADR 0212 §7). Providers are added in System › AI models.
Dialog {
    closeOnQ: false
    title: App.wsProviderTitle
    width: 64
    maxWidthPercent: 90
    helpText: App.wsProviderHelp
    Flex {
        direction: Tui.Vertical
        Text { text: App.wsProviderState }
        Text { text: "provider" }
        ComboBox { id: wsProvider; model: App.wsProviders; textRole: "label"; currentIndex: App.wsProviderIndex }
    }
    DialogButtonBox {
        Button { text: "&Save"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "&Cancel"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.saveWorkspaceProvider(wsProvider.currentIndex)
}
