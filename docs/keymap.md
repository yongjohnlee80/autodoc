# Your keys: a reference for people and agents

AutoDoc reads a key-mapping file, `keymap.xml`, beside the selected `config.toml`. The default is
`$XDG_CONFIG_HOME/autodoc/keymap.xml`, or `~/.config/autodoc/keymap.xml`. `--config
/path/config.toml` selects `/path/keymap.xml`. The file is optional: with none, every key is its
default. It is followed while AutoDoc runs: a save applies on the next frame, and creating or
deleting the file counts too.

## Agent workflow

```sh
autodoc --keymap-reference          # this reference, with every command's id
autodoc --check-keymap              # check keymap.xml beside config.toml
autodoc --check-keymap /path/keymap.xml
```

The checker reads the file exactly as AutoDoc does, with no UI or daemon. It prints every problem
with its line, and exits non-zero while there is one. A key that an inner widget takes first is a
warning, not an error.

## The file

The file uses Qt Creator's keyboard-mapping-scheme shape. Each `<shortcut>` names a command by its
id and gives the keys it should have now, in place of its defaults:

```xml
<mapping>
  <shortcut id="autodoc.window.save"><key value="Ctrl+S"/><key value="F2"/></shortcut>
  <shortcut id="autodoc.leader.openRecent"><key value="Shift+R"/></shortcut>
  <shortcut id="autodoc.window.toggleVimKeys"><key value=""/></shortcut>
  <shortcut id="tui.list.down"><key value="Ctrl+N"/></shortcut>
</mapping>
```

- **Moving a key.** Each `<key value="…"/>` is one key, written as Qt writes it: `Ctrl+Shift+S`,
  `Alt+F`, `F10`, `Space`, `Return`. A capital letter is the key with Shift (`Shift+R`), and a
  symbol is the character typed (`?`, not `Shift+/`). One key per value: no chords such as
  `Ctrl+K, Ctrl+C`.
- **Unbinding.** `<key value=""/>` leaves a command with no key.
- **A command you don't name** keeps its default keys.
- **Refused as a whole.** A misspelled id, an unknown element or attribute, a `<key>` with no
  `value`, two commands of one scope on one key, or a key that can't be read is refused with its
  line. The keys you had stay, and the message names the file and line.

## Scopes

A key belongs to a scope, and two commands of one scope can't share a key:

- `global`: the Window's keys (`autodoc.window.*`), answered wherever the keyboard is.
- each card of keys: SPC's `autodoc.leader.*`, the agents' `autodoc.agentsCard.*`, the todos'
  `autodoc.todoKeys.*`.
- each pane with keys of its own: `autodoc.page.*` (the editor's page), `autodoc.explorer.*` and
  `autodoc.links.*` (the explorer's Explorer and Links tabs), `autodoc.todos.*`, `autodoc.htmlPane.*`.
- the dialogs': `autodoc.arrange.*`, `autodoc.picker.*`, `autodoc.goPick.*`, and
  `autodoc.terminal.mode` (a terminal's and an agent's input and Normal mode) and
  `autodoc.terminal.suspend` (the keys kept from their programs, so a program never stops on them).
- golib's widgets: a list, a tree, a table, a text field, a menu, the editor's Vim and Text modes and
  the rest, every one by its id (`tui.list.down`, `tui.editor.vim.normal.undo`). golib's
  `tui/keymap/README.md` lists them all.

A pane's key wins over the Window's while the keyboard is in that pane: moving a Window key onto a
key a pane uses is legal, and the checker warns of it.

## What a file can't move

- **A menu's or a button's underlined letter** (`&File`) is its label's.
- **A plugin's letters** on its SPC p card are the plugin's and yours through their own preference
  (`tui.plugin.<name>.key.<id>`, Options › Preferences).
- **Your Lua keys** (`autodoc.map`) are your scripts'.
- **The key after a prefix** is the prefix's grammar: the second g of gg, the motion after d in the
  editor, Ctrl+n after a terminal's escape prefix. Moving the prefix moves the prefix alone.

## Seeing your keys

F1's help opens with **YOUR KEYS**: every key your file moves, with its line. The cards (SPC's and
the rest) name a moved key by its new keys. A terminal keeps the Window's maximize key
(`autodoc.window.maximizePanel`), its suspend key (`autodoc.terminal.suspend`) and the tab keys
(`autodoc.window.nextPanelTab`, `autodoc.window.previousPanelTab`) from its program, whichever keys
those are now.

## AutoDoc's commands

Every key AutoDoc itself binds, with its default keys. A key on a card is pressed after the key that
opens the card (SPC, then the card's key).

<!-- the commands: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) -->
| id | scope | keys |
| --- | --- | --- |
| `autodoc.window.open.leader` | `global` | Space Ctrl+Space |
| `autodoc.window.movePane.h` | `global` | Ctrl+H |
| `autodoc.window.movePane.j` | `global` | Ctrl+J |
| `autodoc.window.movePane.k` | `global` | Ctrl+K |
| `autodoc.window.movePane.l` | `global` | Ctrl+L |
| `autodoc.window.save` | `global` | Ctrl+S |
| `autodoc.window.newFile` | `global` | Ctrl+N |
| `autodoc.window.openSearch` | `global` | Ctrl+G |
| `autodoc.window.openCommandLine` | `global` | Ctrl+; |
| `autodoc.window.openFind` | `global` | / |
| `autodoc.window.findNext` | `global` | N |
| `autodoc.window.findPrevious` | `global` | Shift+N |
| `autodoc.window.pickWorkspace` | `global` | Ctrl+W |
| `autodoc.window.quit` | `global` | Ctrl+Q |
| `autodoc.window.maximizePanel` | `global` | F11 |
| `autodoc.window.nextPanelTab` | `global` | Ctrl+Tab |
| `autodoc.window.previousPanelTab` | `global` | Ctrl+Shift+Tab |
| `autodoc.window.cycleWidget` | `global` | F6 |
| `autodoc.window.cycleWidgetBack` | `global` | Shift+F6 |
| `autodoc.window.help.open` | `global` | F1 |
| `autodoc.window.zoomWindow.in` | `global` | Ctrl+= |
| `autodoc.window.zoomWindow.out` | `global` | Ctrl+- |
| `autodoc.window.setZoom.100` | `global` | Ctrl+0 |
| `autodoc.window.toggleVimKeys` | `global` | ? |
| `autodoc.page.openFind` | `autodoc.page` | / |
| `autodoc.page.findNext` | `autodoc.page` | N |
| `autodoc.page.findPrevious` | `autodoc.page` | Shift+N |
| `autodoc.explorer.openFind` | `autodoc.explorer` | / |
| `autodoc.explorer.findNext` | `autodoc.explorer` | N |
| `autodoc.explorer.findPrevious` | `autodoc.explorer` | Shift+N |
| `autodoc.links.openFind` | `autodoc.links` | / |
| `autodoc.links.findNext` | `autodoc.links` | N |
| `autodoc.links.findPrevious` | `autodoc.links` | Shift+N |
| `autodoc.links.relationsDepth` | `autodoc.links` | D |
| `autodoc.htmlPane.openPaneInBrowser` | `autodoc.htmlPane` | B |
| `autodoc.htmlPane.closeHTMLPane` | `autodoc.htmlPane` | Q |
| `autodoc.leader.openSearch` | `autodoc.leader` | / Space |
| `autodoc.leader.openPicker` | `autodoc.leader` | O |
| `autodoc.leader.openFileDialog` | `autodoc.leader` | F |
| `autodoc.leader.openRecent` | `autodoc.leader` | R |
| `autodoc.leader.followLink` | `autodoc.leader` | J |
| `autodoc.leader.goBack` | `autodoc.leader` | B |
| `autodoc.leader.openBuffers` | `autodoc.leader` | Shift+B |
| `autodoc.leader.newFile` | `autodoc.leader` | N |
| `autodoc.leader.closeBuffer` | `autodoc.leader` | Shift+C |
| `autodoc.leader.openOutline` | `autodoc.leader` | Shift+O |
| `autodoc.leader.previewDiagram` | `autodoc.leader` | D |
| `autodoc.leader.previewHTML` | `autodoc.leader` | Shift+H |
| `autodoc.leader.toggleExplorer` | `autodoc.leader` | E |
| `autodoc.leader.toggleRelations` | `autodoc.leader` | L |
| `autodoc.leader.toggleTerminal` | `autodoc.leader` | ` |
| `autodoc.leader.openTodos` | `autodoc.leader` | X |
| `autodoc.leader.maximizePanel` | `autodoc.leader` | Shift+M |
| `autodoc.leader.openNotices` | `autodoc.leader` | H |
| `autodoc.leader.toggleAgent` | `autodoc.leader` | G |
| `autodoc.leader.openAgentProfiles` | `autodoc.leader` | Shift+G |
| `autodoc.leader.help.open` | `autodoc.leader` | ? |
| `autodoc.leader.about.open` | `autodoc.leader` | Shift+A |
| `autodoc.leader.quit` | `autodoc.leader` | Shift+Q |
| `autodoc.leader.pickWorkspace` | `autodoc.leader` | W |
| `autodoc.leader.manageWorkspaces` | `autodoc.leader` | Shift+W |
| `autodoc.leader.openAIModels` | `autodoc.leader` | S |
| `autodoc.leader.openPrefs` | `autodoc.leader` | , |
| `autodoc.leader.toggleKeymap` | `autodoc.leader` | K |
| `autodoc.leader.leaderMenuBar` | `autodoc.leader` | M |
| `autodoc.leader.openWidgets` | `autodoc.leader` | Tab |
| `autodoc.leader.openContextMenu` | `autodoc.leader` | . |
| `autodoc.leader.toggleStatusLine` | `autodoc.leader` | T |
| `autodoc.leader.arrangePanel` | `autodoc.leader` | Shift+L |
| `autodoc.leader.open.agentsCard` | `autodoc.leader` | A |
| `autodoc.agentsCard.run.lua.agent-buffer` | `autodoc.agentsCard` | B |
| `autodoc.agentsCard.run.lua.agent-forward` | `autodoc.agentsCard` | F |
| `autodoc.agentsCard.toggleAgent` | `autodoc.agentsCard` | G |
| `autodoc.agentsCard.openAgentProfiles` | `autodoc.agentsCard` | Shift+G |
| `autodoc.leader.openPluginChooser` | `autodoc.leader` | P |
| `autodoc.todos.todoAdd` | `autodoc.todos` | A |
| `autodoc.todos.todoRemove` | `autodoc.todos` | D |
| `autodoc.todos.todoChooseStatus` | `autodoc.todos` | S |
| `autodoc.todos.todoToggleDetail` | `autodoc.todos` | I O |
| `autodoc.todos.todoAssign` | `autodoc.todos` | Shift+A |
| `autodoc.todos.todoTabBefore` | `autodoc.todos` | [ |
| `autodoc.todos.todoTabAfter` | `autodoc.todos` | ] |
| `autodoc.todos.todoMonthsBack` | `autodoc.todos` | Backspace |
| `autodoc.todos.todoFocusStore` | `autodoc.todos` | Shift+S |
| `autodoc.todos.todoNew` | `autodoc.todos` | Shift+N |
| `autodoc.todos.todoLoad` | `autodoc.todos` | Shift+O |
| `autodoc.todos.todoLabel` | `autodoc.todos` | Shift+L |
| `autodoc.todos.todoRefresh` | `autodoc.todos` | Shift+R |
| `autodoc.todos.todoMove` | `autodoc.todos` | Shift+M |
| `autodoc.todos.open.todoKeys` | `autodoc.todos` | ? |
| `autodoc.todos.todosTab.0` | `autodoc.todos` | 1 |
| `autodoc.todos.todosTab.1` | `autodoc.todos` | 2 |
| `autodoc.todos.todosTab.2` | `autodoc.todos` | 3 |
| `autodoc.todos.todosTab.3` | `autodoc.todos` | 4 |
| `autodoc.todos.todosTab.4` | `autodoc.todos` | 5 |
| `autodoc.todos.todosTab.5` | `autodoc.todos` | 6 |
| `autodoc.todos.todoVarEdit` | `autodoc.todos` | E |
| `autodoc.todos.todoVarMachine` | `autodoc.todos` | M |
| `autodoc.todoKeys.todoEnter` | `autodoc.todoKeys` | Enter |
| `autodoc.todoKeys.todoMonthsBack` | `autodoc.todoKeys` | Backspace |
| `autodoc.todoKeys.todoToggleDetail` | `autodoc.todoKeys` | I O |
| `autodoc.todoKeys.todoTabBefore` | `autodoc.todoKeys` | [ |
| `autodoc.todoKeys.todoTabAfter` | `autodoc.todoKeys` | ] |
| `autodoc.todoKeys.todosTab.0` | `autodoc.todoKeys` | 1 |
| `autodoc.todoKeys.todosTab.1` | `autodoc.todoKeys` | 2 |
| `autodoc.todoKeys.todosTab.2` | `autodoc.todoKeys` | 3 |
| `autodoc.todoKeys.todosTab.3` | `autodoc.todoKeys` | 4 |
| `autodoc.todoKeys.todosTab.4` | `autodoc.todoKeys` | 5 |
| `autodoc.todoKeys.todosTab.5` | `autodoc.todoKeys` | 6 |
| `autodoc.todoKeys.todoChooseStatus` | `autodoc.todoKeys` | S |
| `autodoc.todoKeys.todoAdd` | `autodoc.todoKeys` | A |
| `autodoc.todoKeys.todoRemove` | `autodoc.todoKeys` | D |
| `autodoc.todoKeys.todoAssign` | `autodoc.todoKeys` | Shift+A |
| `autodoc.todoKeys.todoVarEdit` | `autodoc.todoKeys` | E |
| `autodoc.todoKeys.todoVarMachine` | `autodoc.todoKeys` | M |
| `autodoc.todoKeys.todoFocusStore` | `autodoc.todoKeys` | Shift+S |
| `autodoc.todoKeys.todoNew` | `autodoc.todoKeys` | Shift+N |
| `autodoc.todoKeys.todoLoad` | `autodoc.todoKeys` | Shift+O |
| `autodoc.todoKeys.todoLabel` | `autodoc.todoKeys` | Shift+L |
| `autodoc.todoKeys.todoRefresh` | `autodoc.todoKeys` | Shift+R |
| `autodoc.todoKeys.todoMove` | `autodoc.todoKeys` | Shift+M |
| `autodoc.leader.luaKeys.open` | `autodoc.leader` | U |
| `autodoc.arrange.left` | `autodoc.arrange` | H Left |
| `autodoc.arrange.right` | `autodoc.arrange` | L Right |
| `autodoc.arrange.up` | `autodoc.arrange` | K Up |
| `autodoc.arrange.down` | `autodoc.arrange` | J Down |
| `autodoc.arrange.narrower` | `autodoc.arrange` | Shift+H Shift+Left |
| `autodoc.arrange.wider` | `autodoc.arrange` | Shift+L Shift+Right |
| `autodoc.arrange.shorter` | `autodoc.arrange` | Shift+K Shift+Up |
| `autodoc.arrange.taller` | `autodoc.arrange` | Shift+J Shift+Down |
| `autodoc.arrange.keep` | `autodoc.arrange` | Enter |
| `autodoc.picker.field` | `autodoc.picker` | Ctrl+K |
| `autodoc.picker.list` | `autodoc.picker` | Ctrl+J Ctrl+H |
| `autodoc.picker.preview` | `autodoc.picker` | Ctrl+L |
| `autodoc.goPick.next` | `autodoc.goPick` | Tab |
| `autodoc.goPick.previous` | `autodoc.goPick` | Shift+Tab |
| `autodoc.terminal.mode` | `autodoc.terminal` | Ctrl+T |
| `autodoc.terminal.suspend` | `autodoc.terminal` | Ctrl+Z |
<!-- end of the generated commands -->
