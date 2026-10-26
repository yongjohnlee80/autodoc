# Commands: a reference for people and agents

Every command AutoDoc is built with, by its id. Those with a `:` name run from the command line (`:` in
Normal mode), and an agent runs them with `autodoc --editor editor.run '[":name args"]'`. The id runs any
of them from Lua (`autodoc.run("id", …)`) and binds it to a key (`autodoc --keymap-reference`).

## Agent workflow

```sh
autodoc --commands-reference                      # this reference: the commands AutoDoc is built with
autodoc --editor editor.commands                  # the commands of the AutoDoc that started you, your
                                                  # Lua and plugin commands included; no permission
autodoc --editor editor.open '["notes/today.md"]' # open a file: absolute, or relative to the
                                                  # workspace the editor is in
autodoc --editor editor.run '[":e notes/today.md"]'
```

Check the list before you run a command rather than guess one: `editor.run` refuses a name no command
has, and says so.

Lua and plugin commands are not in this table: they come from the scripts and plugins of the user's
configuration. `editor.commands` lists them with the rest.

## The commands

<!-- the commands: generated from the catalog (go test ./tui -run TestTheCommandsReferenceIsGenerated -update) -->
| Command | `:` name | Arguments | What it does |
|---|---|---|---|
| `agent.setup` |  |  | set up an agent |
| `agent.toggle` |  |  | AI agent |
| `app.command_line` |  |  | the command line |
| `app.quit` |  |  | quit |
| `app.restart_backend` |  |  | restart the backend |
| `buffer.close` |  |  | close the buffer |
| `buffer.close_left` |  |  | close the buffers to the left |
| `buffer.close_right` |  |  | close the buffers to the right |
| `buffer.delete` | `:bd` |  | close the shown buffer; unsaved changes are asked about |
| `buffer.go` | `:b` | `<number\|name>` | show the buffer of that number, or whose name holds the text; Tab lists them |
| `buffer.new` |  |  | a new buffer, an unsaved draft: its arguments are its suggested name, then its text |
| `buffer.pick` |  |  | switch buffer |
| `buffer.show_1` |  |  | show the 1st buffer |
| `buffer.show_10` |  |  | show the 10th buffer |
| `buffer.show_2` |  |  | show the 2nd buffer |
| `buffer.show_3` |  |  | show the 3rd buffer |
| `buffer.show_4` |  |  | show the 4th buffer |
| `buffer.show_5` |  |  | show the 5th buffer |
| `buffer.show_6` |  |  | show the 6th buffer |
| `buffer.show_7` |  |  | show the 7th buffer |
| `buffer.show_8` |  |  | show the 8th buffer |
| `buffer.show_9` |  |  | show the 9th buffer |
| `editor.context_menu` |  |  | the right-click menu |
| `editor.mode` |  |  | the editor mode (Vim, Text) |
| `editor.vim_keys` |  |  | the Vim keys' card |
| `explorer.toggle` |  |  | the explorer |
| `file.back` |  |  | back |
| `file.follow_link` |  |  | go to the link under the cursor |
| `file.new` |  |  | new file |
| `file.open` |  |  | open a document |
| `file.open_any` |  |  | open a file anywhere |
| `file.open_default_app` |  |  | open with the default app |
| `file.open_path` | `:open`, `:e`, `:edit` | `<path>` | open a file by its path, as File › Open any file does once it is chosen: on the command line absolute, ~/… or relative to the workspace in use; from Lua, a key or a plugin, absolute |
| `file.open_recent` |  |  | recent files |
| `file.outline` |  |  | the file's outline |
| `file.preview_diagram` |  |  | view the diagram |
| `file.preview_html` |  |  | preview HTML |
| `file.reload` |  |  | reload from disk |
| `file.save` |  |  | save |
| `find.clear` | `:nohl` |  | clear the find |
| `find.next` |  |  | find next |
| `find.open` |  |  | find in the page |
| `find.previous` |  |  | find previous |
| `find.term` | `:find` | `<term>` | find term in the page; n and N go to the next and previous |
| `help.about` |  |  | about |
| `help.keys` |  |  | help |
| `help.tutorial` |  |  | the tutorial |
| `kbtype.apply` |  | `<name or git URL>` | create a KB type's folders and files in the open workspace, keeping every file already there: autovim.kb, zettelkasten.kb, para.kb, slipbox.kb, or a type's git URL |
| `links.toggle` |  |  | the links |
| `notices.open` |  |  | notifications |
| `panel.arrange` |  |  | arrange a panel |
| `panel.maximize` |  |  | maximize or restore the terminal or agents |
| `panel.reset_layout` |  |  | reset the panel layout |
| `panel.resume` |  |  | resume the terminal's or the agents' tab (SIGCONT) |
| `plugins.add` |  |  | add a plugin by its registry name or a git URL |
| `plugins.browse` |  |  | browse the registry's plugins and Lua packages |
| `plugins.commands` |  |  | plugin commands: choose a plugin, then one of its commands |
| `plugins.manage` |  |  | manage plugins |
| `plugins.update` |  | `<name>` | update a plugin by its name, as Manage plugins › Update does: its new commit is asked about first. A plugin may name only itself |
| `prefs.agent_profiles` |  |  | agent profiles |
| `prefs.editor` |  |  | editor preferences |
| `prefs.fonts` |  |  | fonts and zoom |
| `prefs.search_models` |  |  | search models |
| `prefs.set_up_semantic_search` |  |  | set up semantic search |
| `prefs.terminal_panel_keys` |  |  | Ctrl+h/j/k/l move between panels while typing |
| `results.copy` |  |  | results: copy selection (the selected cells, or the cursor's, as TSV) |
| `results.copy_csv` |  |  | results: copy selection as CSV (its fields as a header) |
| `results.copy_json` |  |  | results: copy selection as JSON (an object per row) |
| `results.copy_markdown` |  |  | results: copy selection as a Markdown table |
| `results.csv` |  |  | results: export loaded rows as CSV… |
| `results.export` |  |  | results: export loaded rows… |
| `results.markdown` |  |  | results: insert loaded rows as a Markdown table |
| `results.select_all` |  |  | results: select every loaded row |
| `results.sort_asc` |  | `[field]` | results: sort the loaded rows by a field (its 1-based position; the cursor's when none), ascending |
| `results.sort_desc` |  | `[field]` | results: sort the loaded rows by a field (its 1-based position; the cursor's when none), descending |
| `results.toggle` |  |  | results panel |
| `results.unsort` |  |  | results: unsort, the rows in the query's order |
| `scripts.install` | `:lua-install` | `<name or git-url>` | clone a Lua package into the config folder's lua/, once you agree |
| `scripts.reload` | `:lua-reload` |  | load every Lua script again: AutoDoc's, then the config folder's lua/ |
| `scripts.shell` | `:lua` | `[chunk]` | alone, a Lua shell in the terminal; with a chunk, run it and show its values |
| `search.open` |  |  | search the workspace |
| `terminal.open` | `:terminal`, `:term` |  | open the terminal (SPC `) |
| `terminal.toggle` |  |  | the terminal |
| `todos.label_store` |  |  | label the todo store |
| `todos.load_store` |  |  | load a todo store |
| `todos.new_store` |  |  | a new todo store |
| `todos.open` | `:todos` |  | the todos panel, open or closed (SPC x) |
| `view.hide_menu_bar` |  |  | hide the menu bar |
| `view.image_previews` |  |  | image previews |
| `view.line_numbers` |  |  | line numbers |
| `view.menu` |  |  | show/focus or hide the menu |
| `view.redraw` |  |  | lay the Rendered view out again, the note unchanged: what it draws from beside the note changed (a lab note's cell outputs, which the Jupyter plugin runs this for) |
| `view.status_line` |  |  | the status line |
| `view.wrap` |  |  | wrap long lines |
| `widget.go` | `:go` | `<name>` | put the keyboard on a widget: a panel opens if closed; Tab lists the names; alone, the Go-to picker |
| `window.layout` | `:layout` | `[window]` | Tiled at an edge, or Float, for a window: alone, the editor's; Tab lists the windows |
| `workspace.manage` |  |  | manage workspaces |
| `workspace.switch` |  |  | switch workspace |
| — | `:q`, `:quit` |  | quit; q! discards unsaved changes |
| — | `:replace` | `<term> <with>` | replace each match, asking y / n; q or Esc stops |
| — | `:w`, `:write` |  | save |
| — | `:wq` |  | save, then quit |
| — | `:x`, `:exit` |  | save if modified, then quit |
<!-- end of the generated commands -->
