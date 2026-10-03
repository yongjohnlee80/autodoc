Dialog {
    closeOnQ: false
    title: App.patternTitle
    width: 76
    maxWidthPercent: 90
    helpText: App.patternHelp
    Flex {
        direction: Tui.Vertical
        Text { text: App.patternRoot }
        Text { text: "include globs (semicolon-separated; blank matches no files)" }
        TextField { id: includeRules; text: App.patternInclude }
        Text { text: "exclude globs (semicolon-separated)" }
        TextField { id: excludeRules; text: App.patternExclude }
    }
    DialogButtonBox {
        Button { text: "&Save"; DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole }
        Button { text: "&Cancel"; DialogButtonBox.buttonRole: DialogButtonBox.RejectRole }
    }
    onAccepted: App.savePatterns(includeRules.text, excludeRules.text)
}
