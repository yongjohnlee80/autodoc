// Todos.qml — Todos › Open todos… (SPC x): the todo stores, AutoDoc's own view of them (todos.go).
// New, Load and Close along the top; the stores known on the left, the open one marked ● when the
// daemon writes it; on the right a tab per bucket, each a list and the task under the cursor's
// details. The Archived tab lists its months, and a month's tasks only once it is opened. The keys
// are autofinder's todos panel's, live while this is open: Enter opens the task's file.
Dialog {
    closeOnQ: true
    maxWidthPercent: 96
    maxHeightPercent: 94
    title: App.todosTitle
    dim: false
    Shortcut { sequence: "a"; onActivated: App.todoAdd() }
    Shortcut { sequence: "d"; onActivated: App.todoRemove() }
    Shortcut { sequence: "s"; onActivated: App.todoCycle() }
    Shortcut { sequence: "i"; onActivated: App.todoToggleDetail() }
    Shortcut { sequence: "o"; onActivated: App.todoToggleDetail() }
    Shortcut { sequence: "Shift+R"; onActivated: App.todoRefresh() }
    Shortcut { sequence: "Shift+M"; onActivated: App.todoMove() }
    Shortcut { sequence: "?"; onActivated: App.todoToggleHelp() }
    // a Dialog gives the keyboard to its first control (New…): each tab's list takes it here
    onOpened: todoOpenTable.forceActiveFocus()
    Shortcut { sequence: "1"; onActivated: { App.todosTab(0); todoOpenTable.forceActiveFocus() } }
    Shortcut { sequence: "2"; onActivated: { App.todosTab(1); todoInProgressTable.forceActiveFocus() } }
    Shortcut { sequence: "3"; onActivated: { App.todosTab(2); todoDeferredTable.forceActiveFocus() } }
    Shortcut { sequence: "4"; onActivated: { App.todosTab(3); todoCompletedTable.forceActiveFocus() } }
    Shortcut { sequence: "5"; onActivated: { App.todosTab(4); todoMonths.forceActiveFocus() } }
    Shortcut { sequence: "Backspace"; onActivated: todoMonths.forceActiveFocus() }
    Flex {
        direction: Tui.Vertical
        Flex {
            direction: Tui.Horizontal
            Button { text: "&New…"; onClicked: App.todoNew() }
            Text { text: " " }
            Button { text: "&Load…"; onClicked: App.todoLoad() }
            Text { text: " " }
            Button { text: "Close (&q)"; onClicked: todosDialog.close() }
        }
        Split {
            orientation: Tui.Horizontal
            ratio: 0.24
            Layout.fillHeight: true
            Frame {
                title: "stores"
                TableView {
                    id: todoStores
                    palette.highlight: Theme.document.highlight
                    palette.highlightedText: Theme.document.highlightedText
                    model: App.todoStores
                    onActivated: App.todoStoreChosen(index)
                    TableViewColumn { role: "mark"; title: ""; width: 2 }
                    TableViewColumn { role: "name"; title: "PROJECT"; width: 0 }
                }
            }
            TabView {
                id: todoTabs
                currentIndex: App.todoTab
                onCurrentIndexChanged: App.todosTab(index)
                Tab {
                    title: "Open"
                    Split {
                        orientation: Tui.Horizontal
                        ratio: 0.6
                        TableView {
                            id: todoOpenTable
                            palette.highlight: Theme.document.highlight
                            palette.highlightedText: Theme.document.highlightedText
                            model: App.todoOpen
                            onCurrentIndexChanged: App.todoMoved(index)
                            onActivated: App.todoActivated(index)
                            TableViewColumn { role: "n"; title: "#"; width: 3 }
                            TableViewColumn { role: "errors"; title: ""; width: 1 }
                            TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                            TableViewColumn { role: "priority"; title: "PRIO"; width: 6 }
                            TableViewColumn { role: "assignee"; title: "ASSIGNEE"; width: 14 }
                        }
                        Frame { title: "details"; visible: App.todoDetailShown; Text { text: App.todoDetail; wrapMode: Tui.WordWrap } }
                    }
                }
                Tab {
                    title: "In progress"
                    Split {
                        orientation: Tui.Horizontal
                        ratio: 0.6
                        TableView {
                            id: todoInProgressTable
                            palette.highlight: Theme.document.highlight
                            palette.highlightedText: Theme.document.highlightedText
                            model: App.todoInProgress
                            onCurrentIndexChanged: App.todoMoved(index)
                            onActivated: App.todoActivated(index)
                            TableViewColumn { role: "errors"; title: ""; width: 1 }
                            TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                            TableViewColumn { role: "priority"; title: "PRIO"; width: 6 }
                            TableViewColumn { role: "assignee"; title: "ASSIGNEE"; width: 14 }
                        }
                        Frame { title: "details"; visible: App.todoDetailShown; Text { text: App.todoDetail; wrapMode: Tui.WordWrap } }
                    }
                }
                Tab {
                    title: "Deferred"
                    Split {
                        orientation: Tui.Horizontal
                        ratio: 0.6
                        TableView {
                            id: todoDeferredTable
                            palette.highlight: Theme.document.highlight
                            palette.highlightedText: Theme.document.highlightedText
                            model: App.todoDeferred
                            onCurrentIndexChanged: App.todoMoved(index)
                            onActivated: App.todoActivated(index)
                            TableViewColumn { role: "errors"; title: ""; width: 1 }
                            TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                            TableViewColumn { role: "priority"; title: "PRIO"; width: 6 }
                            TableViewColumn { role: "due"; title: "DUE"; width: 11 }
                        }
                        Frame { title: "details"; visible: App.todoDetailShown; Text { text: App.todoDetail; wrapMode: Tui.WordWrap } }
                    }
                }
                Tab {
                    title: "Completed"
                    Split {
                        orientation: Tui.Horizontal
                        ratio: 0.6
                        TableView {
                            id: todoCompletedTable
                            palette.highlight: Theme.document.highlight
                            palette.highlightedText: Theme.document.highlightedText
                            model: App.todoCompleted
                            onCurrentIndexChanged: App.todoMoved(index)
                            onActivated: App.todoActivated(index)
                            TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                            TableViewColumn { role: "assignee"; title: "ASSIGNEE"; width: 14 }
                        }
                        Frame { title: "details"; visible: App.todoDetailShown; Text { text: App.todoDetail; wrapMode: Tui.WordWrap } }
                    }
                }
                Tab {
                    title: "Archived"
                    // the months on the left, read from the folders; a month's tasks on the right,
                    // read when Enter opens it
                    Split {
                        orientation: Tui.Horizontal
                        ratio: 0.2
                        TableView {
                            id: todoMonths
                            palette.highlight: Theme.document.highlight
                            palette.highlightedText: Theme.document.highlightedText
                            model: App.todoMonths
                            onActivated: { App.todoMonthChosen(index); todoArchivedTable.forceActiveFocus() }
                            TableViewColumn { role: "month"; title: "MONTH"; width: 8 }
                            TableViewColumn { role: "count"; title: "TASKS"; width: 0 }
                        }
                        Split {
                            orientation: Tui.Horizontal
                            ratio: 0.6
                            TableView {
                                id: todoArchivedTable
                                palette.highlight: Theme.document.highlight
                                palette.highlightedText: Theme.document.highlightedText
                                model: App.todoArchived
                                onCurrentIndexChanged: App.todoMoved(index)
                                onActivated: App.todoActivated(index)
                                TableViewColumn { role: "title"; title: "TITLE"; width: 0 }
                                TableViewColumn { role: "assignee"; title: "ASSIGNEE"; width: 14 }
                            }
                            Frame { title: "details"; visible: App.todoDetailShown; Text { text: App.todoDetail; wrapMode: Tui.WordWrap } }
                        }
                    }
                }
            }
        }
        Text { text: App.todosStatus; wrapMode: Tui.WordWrap }
        Text {
            visible: App.todoHelpShown
            wrapMode: Tui.WordWrap
            text: "Enter open the task's file   a add   d remove   s status (open → completed → deferred → open)   i, o details   R refresh   M move the store   1–5 a tab   Backspace the months (Archived)   ? these keys   q close"
        }
    }
}
