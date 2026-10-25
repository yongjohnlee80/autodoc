# Chapter 3: Modal Editor and Panels

Both `autodoc --ui` (the terminal TUI) and `autodoc --gui` (the native desktop application) show the same thing: one **page**, 120 columns wide and centred, and **panels** that open over it from its sides. The page edits in one of two editor modes, **Vim** (modal, the default) or **Text** (modeless). Every open file is a **buffer**, and the leader key, `SPC`, reaches the rest.

---

## 3.1 The Page

- **Width**: the page is 120 columns wide and centred, with the ruler at its edge. Preferences sets the width.
- **Wrap and line numbers**: long lines wrap at the page's width. Line numbers can show. Both are under View › Wrap long lines and View › Line numbers, or in Preferences.
- **The breadcrumb**: the page's frame names the file, then where the cursor is, separated by `›`. In Markdown that is the headings above the cursor; in YAML, the key path.
- **The outline**: `SPC O` (Go › Outline…) lists the file's headings, unsaved ones too, and `Enter` jumps there.
- **Frontmatter**: once the workspace has a schema, the line over the page names a file's frontmatter problems. In the GUI's Rendered View, a note's frontmatter is hidden.

![AutoDoc's Leader Keys on SPC's Card, by Group](leader-keys-by-group.chart.json)

The chart counts the keys SPC's card lists (§3.4), as `tui/keys.go` binds them.

---

## 3.2 The Editor Modes: Vim and Text

The page edits in the **Vim** mode (modal, the default) or the **Text** mode (modeless). `SPC k` switches between them, and so does Options › Editor mode. In the Text mode, `Ctrl+Space` is the leader, so the switch is `Ctrl+Space k`. The choice is a preference the daemon keeps, so `--ui` and `--gui` share it.

![The Editor's Modes and Views](editor-mode-state-machine.mmd)

### The Vim mode

1. **Normal**:
   - motions: `h` `j` `k` `l`, `w` `b` `e`, `0` `$`, `gg` `G`, and `{` `}` by paragraph (`[` and `]` move by paragraph too);
   - operators: `d`, `c`, `y`, `>`, `<`, and `g~` `gu` `gU`;
   - text objects: `iw` `aw`, `iW` `aW`, `i"` `i'`, `` i` ``, `i(` (`ib`), `i{` (`iB`), `i[`, `i<`, `ip` (a paragraph) and `is` (a sentence);
   - `.` repeats the last change;
   - `H` and `L` show the buffer before or after the shown one;
   - `Ctrl+O` and `Ctrl+I` step back and forward along the jumps, across documents too;
   - `?` shows the Vim keys' card.
2. **Insert**: entered with `i`, `a`, `I`, `A`, `o` or `O`; `Esc` goes back to Normal. In the GUI's Rendered View, `Enter` on a Markdown list item continues the list, and `Tab` / `Shift+Tab` nest the item or bring it out.
3. **Visual**: character-wise `v`, line-wise `V`, or a block with `Ctrl+v`. `>` and `<` shift the lines selected.
4. **The command line**: `:` opens it in Normal mode, and `Ctrl+;` opens it in either editor mode. Its commands are in §3.8.

### The Text mode

Typing types. The keys are an ordinary text editor's:

- `Ctrl+S` saves;
- `Ctrl+Z` / `Ctrl+Y` undo and redo;
- `Ctrl+X` / `Ctrl+C` / `Ctrl+V` cut, copy and paste;
- `Ctrl+A` selects all;
- `Ctrl+G` searches the workspace;
- `Ctrl+T` toggles the Rendered View (`--gui` only: the terminal's `--ui` has no Rendered View);
- `Ctrl+Space` opens SPC's card;
- `Ctrl+;` opens the command line.

Search › Find in page finds in the page.

---

## 3.3 Buffers

Every file opened is a buffer, and the GUI shows them as tabs. The status line (`SPC t`) shows `[+]` while the shown one has unsaved changes.

- `H` and `L` in Normal mode show the buffer before or after the shown one, in the order of the GUI's tabs, wrapping around.
- `SPC B` or `:b` switches buffer from a picker, each buffer previewed with its unsaved text. `:b <number>` or `:b <name>` goes straight to one.
- `SPC C` or `:bd` closes the shown buffer, asking first over unsaved changes.

AutoDoc shows one page at a time: there are no window splits, and no folding. The panels (§3.5) and the GUI's HTML preview pane (`SPC H`) sit beside or over it.

---

## 3.4 Leader (`SPC`) Chords and Command Pickers

In Normal mode, pressing the **Spacebar (`SPC`)** opens SPC's card, listing every leader key by group. A capital is the key with Shift (`SPC O` is Shift+O). The keys, as `tui/keys.go` and AutoDoc's features bind them:

- **Find:** `SPC /` (or `SPC SPC`) searches the workspace; `SPC o` opens a document (a filter over the workspace's files, with a preview); `SPC f` opens a file anywhere; `SPC r` lists recent files; `SPC j` goes to the link under the cursor; `SPC b` goes back; `SPC B` switches buffer.
- **File:** `SPC n` a new file; `SPC C` closes the buffer; `SPC O` the file's outline; `SPC d` views the diagram; `SPC H` previews HTML.
- **Panels:** `SPC e` the explorer; `SPC l` the links (Relations); `` SPC ` `` the terminal; `SPC x` the todos; `SPC M` maximizes a panel; `SPC h` the notifications.
- **Agents:** `SPC g` the AI agent; `SPC G` the agent profiles; `SPC a` the agents' card, where `SPC a b` sends the page to an agent and `SPC a f` the selection or the clipboard.
- **Manage:** `SPC w` switches workspace; `SPC W` manages workspaces; `SPC s` the search models; `SPC ,` the editor preferences; `SPC k` the editor mode (Vim, Text).
- **Window:** `SPC m` shows, focuses or hides the menu; `SPC .` the right-click menu; `SPC t` the status line; `SPC L` arranges a panel; `SPC p` a plugin's commands; `SPC u` your Lua keys; and `SPC Tab`, below.
- **Help:** `SPC ?` help; `SPC A` about; `SPC Q` quit.
- **`SPC Tab` (Go to a widget)**: Opens the Go-to picker over the places the keyboard can go: the editor, every panel, and each terminal and agent tab, never the menu bar. Tab and Shift+Tab move its cursor; Enter goes there, opening a closed panel. `:go <name>` does the same from the command line (`:go explorer`, `:go terminal/lua`), and **F6 / Shift+F6** step the keyboard through the open widgets.
- **The dock (the menu bar's right end)**: one icon per widget. **✎** is the editor, and it comes first. Then comes each panel that is open, or running on with its drawer closed: a terminal with a shell, an agent, a plugin card whose plugin runs. A tab belongs to its panel's icon. A click on a shown panel hides it, not closed: it keeps everything it holds (a GlobeMap keeps its picture, view and filter), its icon is dimmed, and the keyboard goes back to the editor. A click on a hidden panel shows it again as it was, with the keyboard in it, as the Go-to picker does; a closed panel opens. **✎** hides every shown panel and remembers them; its next click shows the same ones again, unless a panel was opened, shown or closed in between. Opening a note from the GlobeMap, the Mindmap or the Links drawer hides it the same way, so the dock brings it back. A hidden panel leaves the dock only when it is closed for real. With the bar on auto-hide, it stays shown while the dock lists more than the editor, and the page moves down a row while it does.

Some keys work wherever the keyboard is: `Ctrl+h/j/k/l` move between the page and the panels (in Normal mode), `Ctrl+S` saves, `Ctrl+G` searches, `Ctrl+W` switches workspace (in the Vim mode's Insert, `Ctrl+W` deletes the word before the cursor: `Esc` first), `F1` opens the help, `F11` maximizes the terminal or the agents, and `Ctrl+Q` quits. `Ctrl+Z` never reaches a program in the terminal or the agents, so it can't stop one by accident.

![Complete Modal Editor, Leader Chord, and Panel Keybinding Reference](complete-keybinding-reference.csv)

---

## 3.5 The Panels

The explorer, the links, the todos, the terminal and the AI agent each open over the page from an edge. Preferences sets the edge, and `Escape` or the panel's key again closes it.

- **Moving one:** every panel moves and resizes.
  - Alt (Option on a Mac) with the left mouse button drags it anywhere, and Alt with the right button resizes it.
  - `SPC L` (Go › Arrange panel…) does both from the keyboard: `h` `j` `k` `l` or the arrows move it, `H` `J` `K` `L` resize it, `Enter` keeps it, and `Esc` puts it back.
  - Go › Reset panel layout docks every panel at its edge again.
- **Maximizing:** `SPC M` or `F11` maximizes or restores the terminal or the agents: the one with the keyboard, else the newest open. The other panels don't maximize.

### Tiled or floating: the layout

The page is a window of its own, and the explorer, the terminal, the agent, the results and the todos are each either **floating** (over the page, docked at an edge or anywhere you drag them, as above) or **tiled** (beside the page, sharing the screen with it). Each window's **Layout** says which.

- **Choosing it:** Layout › Tiled or Float is on every window's menu, right above Go to ›:
  - the page's buffer strip (`--gui`);
  - the terminal's and the agent's tabs (`≡`, or a right click on the strip);
  - the explorer's and the todos' tabs (`≡`);
  - the results table's right-click menu, at its end.

  `:layout [window]` opens it over the page, the page's when no window is named. Tab completes the windows: `editor`, `explorer`, `terminal`, `agent`, `results`, `todos`.
- **The defaults:** the page is tiled, every panel floats. A layout is kept across restarts.
- **Where a tiled panel goes:** at its tile's edge, a share of the screen across it. These are the preferences `tui.<window>.tileEdge` and `tui.<window>.tileSize` (a percent, 5 to 90). Alt with the right button drags the border a tiled panel shares, and its new share is kept. A tiled panel's own docked place and floating rectangle stay as they were, for when it floats again.
- **When the room runs out:** the page needs 40 columns and 8 rows, and a panel what its content needs at the left or right, or at the top or bottom: the explorer 25 columns or 4 rows, the terminal and the agent 27 columns or 6 rows, the results 20 columns or 7 rows, the todos 20 columns or 16 rows. When the window is too small for the page and every tiled panel, the panel tiled last floats until the room comes back, then tiles again by itself. Its Layout still says Tiled: that is your choice, not the room's.
- **A floating page** moves and resizes up to the page's width, never closes, and comes forward when it takes the keyboard. When it is minimized, the dock's editor icon shows it again.
- **Moving between them:** `Ctrl+h` `j` `k` `l` go to the window placed that way: tiled, docked or floating alike.
- **Arranging:** `SPC L` moves a floating or docked panel. A tiled one is placed by its tile, so `SPC L` says so and leaves it.

![The Page and the Panels Around It](panel-docking-layout.mmd)

### The panels

1. **The explorer (`SPC e`)**: every workspace's folders and files. `Enter` on a file opens it and closes the panel.
2. **The links (`SPC l`)**: what this file is connected to, by kind:
   - superseded by, supersedes, sources, cited by, amends, amended by, related and `adr`;
   - its links and its backlinks;
   - then what names nothing, and the supersession loops.

   `d` shows the files two links away, under the one that reaches them.
3. **The todos (`SPC x`)**: the workspace's `.todo-list/` store, a tab for each status: Open, In progress, Deferred, Completed and Archived. `s` moves the selected task to another status, and `A` assigns it to an agent.
4. **The terminal (`` SPC ` ``)**: your shells, in the workspace's folder. §3.8 says more.
5. **The AI agent (`SPC g`)**: an agent CLI in a terminal of its own, started in AutoDoc's folder for the workspace.
6. **The notifications (`SPC h`, Go › Notifications…)**: a dialog listing what happened (a save, the connection, indexing). It is a dialog, not a panel: it has no edge and doesn't move.
7. **The results (Go › Results)**: a table of rows a plugin returns, such as a query's.
8. **Plugin cards (`SPC p`)**: a plugin's card, beside the page.
9. **The HTML preview (`SPC H`)**: under `--gui`, a pane beside the editor, the note's page drawn live as you type. In the terminal, a dialog shows the page as an image when View › Image previews is on, the terminal confirmed kitty graphics, and a headless Chromium or Chrome is there; otherwise the page opens in the browser.

The outline (`SPC O`) is a list over the page, not a panel.

---

## 3.6 Preferences, and `config.toml`

The editor's and the panels' settings are **preferences**. Options › Editor preferences (`SPC ,`) sets them:

- the editor mode, wrapping, line numbers, indent marks and the page's width;
- each panel's edge;
- the theme, the toast corner, and what to do with a file changed on disk.

The daemon keeps them (`preference.set`), so `--ui` and `--gui` share them.

`config.toml` (`$XDG_CONFIG_HOME/autodoc/config.toml`, else `~/.config/autodoc/config.toml`) configures the daemon itself. A setting it doesn't know is refused, not ignored:

```toml
[server]
socket = ""        # the unix socket; empty: the default
state_dir = ""     # the state directory; empty: the default
data_dir = ""      # the store's directory; empty: the default

[follow]
poll_interval = "2s"   # how often to list a root that can't be watched (the default)

[embedding_queue]
max_inflight = 1   # embedding requests at once, across every workspace: 1 (the default) or 2
```

### Walkthrough: Reviewing an Architecture Decision

To see how the editor and the panels work together in practice, consider reviewing a proposed ADR (`adrs/0048-hybrid-rrf-fusion.md`) and its linked benchmark task:

1. **Open the Document (`SPC o`)**: Press `SPC o`, type `0048 rrf`, and press `Enter` to open `adrs/0048-hybrid-rrf-fusion.md` in the center page.
2. **Survey Its Structure (`SPC O`)**: Press `SPC O` (Go › Outline…) to list the file's headings, choose `## 3. Reciprocal Rank Fusion Formula`, and press `Enter` to jump there.
3. **Inspect What Links Here (`SPC l`)**: Press `SPC l` to open the **Relations** panel: the ADR's links and its backlinks, such as the synthesis notes and reviews that link to `[[0048-hybrid-rrf-fusion]]`, and a task whose `adr:` names it.
4. **Follow a Link (`SPC j`)**: Move the cursor onto the `[[wikilink]]` naming the benchmark report and press `SPC j` to open it. `SPC b` goes back to the ADR, where you left it.
5. **Check the Diagrams (`Ctrl+T`)**: In `autodoc --gui`, press `Ctrl+T` to switch the page into **Rendered View**, its diagrams, Vega-Lite charts, and CSV tables drawn in place. `Ctrl+T` again shows the source.
6. **Update the Linked Task (`SPC x`)**: Press `SPC x` to open the **Todos** panel, select the review task, and press `s` to choose its new status (the daemon's `todos.status` verb).

---

## 3.7 Files Changed on Disk

AutoDoc is file-canonical, so another program (`git pull`, an AI agent) may change a file you have open:

- **A file with no unsaved changes** reloads in place, as one undo step, the cursor kept. Preferences › files changed on disk can make it ask first.
- **A file with unsaved changes** is marked "changed on disk" (`[!]`). AutoDoc asks about it when you next open or save it: keep editing, reload the disk's version, or overwrite it with yours. After a reload, `u` brings your text back.
- **A save** writes only over the version you opened. If the file changed since, it asks the same.
- **A file deleted on disk** keeps its text, and a save creates it again.

---

## 3.8 The Command Line (`:`)

`:` (or `Ctrl+;`) opens the command line. These are its commands:

```text
:<number>               Go to that line
:w  (:write)            Save
:q  (:quit) / :q!       Quit; :q! discards unsaved changes
:wq                     Save, then quit
:x  (:exit)             Save if modified, then quit
:find <term>            Find a term in the page
:nohl                   Clear the find
:replace <term> <with>  Replace each match, asking y / n; q or Esc stops
:b [<number or name>]   Show a buffer; alone, the buffer picker
:bd                     Close the buffer
:go [<name>]            Go to a widget (:go explorer, :go terminal/lua); alone, the Go-to picker
:todos                  The todos
:terminal  (:term)      Open the terminal
:lua [chunk]            A Lua shell, or run Lua
:lua-reload             Reload the Lua scripts
:lua-install <name or git URL>  Install a Lua package
:table <cols> <rows>    Insert a Markdown table; Tab walks its cells
:mermaid <type>         Insert a Mermaid diagram to start from
:agent-buffer           Send the page to an agent
:agent-forward          Send the selection, or the clipboard, to an agent
:usage                  What AutoDoc uses
```

Every other command has a key or a menu row, and a Lua script reaches any of them with `autodoc.run(id)`. A plugin's commands run from `SPC p`.

### Finding and Replacing

In Normal mode, `/` finds in the page, `n` and `N` go to the next and previous match, and `*` and `#` find the word under the cursor, forward and back. (`?` is the Vim keys' card, not a search.) `:find <term>` finds from the command line, and `:replace <term> <with>` replaces each match, asking `y` or `n` at each.

### The Terminal (`` SPC ` ``)

The terminal runs your shells in the workspace's folder: at the bottom, or wherever Preferences puts it.

- **Tabs:** each shell has a tab. `+` adds one, and a tab's menu has Go to ›, New, Close, Theme and font…, Maximize and Hide.
- **Keys:** every key goes to the shell, `Esc` too. `Ctrl+T` (or `Ctrl+\ Ctrl+n`) leaves for Normal mode, where `h` `j` `k` `l`, `/` and `?` move over the shell's history, `v` selects and `y` copies. `Ctrl+T`, `i` or `Enter` goes back.
- **Closing:** `Escape` in Normal mode, or `` SPC ` `` again, hides the terminal, and its shells run on.

`autodoc --call` reaches the daemon from a shell:

```bash
# Search the workspace "kb" from the terminal
autodoc --call search.query '["kb", "rrf fusion", {"limit": 5}]'
```

---

## 3.9 Moving Your Keys (`keymap.xml`)

Every key AutoDoc binds, and every key its widgets bind, is a command with an id. A `keymap.xml`
beside `config.toml` moves any of them. The full reference, with every id and its default keys, is
[`docs/keymap.md`](../keymap.md), or `autodoc --keymap-reference` on an installed binary.

### Walkthrough: moving search to F5 and recent files to a capital R

1. **Write the file.** Create `~/.config/autodoc/keymap.xml`:

   ```xml
   <mapping>
     <shortcut id="autodoc.window.openSearch"><key value="F5"/></shortcut>
     <shortcut id="autodoc.leader.openRecent"><key value="Shift+R"/></shortcut>
   </mapping>
   ```

2. **Check it.** `autodoc --check-keymap` reads the file as AutoDoc does and prints
   `…/keymap.xml: valid (0 warnings)`. A misspelled id is refused with its line.
3. **Watch it apply.** With AutoDoc open, the save applies on the next frame: F5 opens search,
   and Ctrl+G no longer does. Press `SPC`: the card lists `Shift+R  recent files`.
4. **See what you moved.** F1's help opens with **YOUR KEYS**, each moved command with its file and
   line.
5. **Break it on purpose.** Change `F5` to `F55` and save: a notice names `keymap.xml, line 2`, and
   F5 still opens search. A refused file changes nothing.

Unbind a key with `<key value=""/>`. Give a command several keys with several `<key>` elements. A
menu's underlined letter, a plugin's letters (their own preference) and your Lua keys can't be moved
here; nor can the key after a prefix, such as the second g of gg.

---

[← Previous: Chapter 02: Quickstart & Workspaces](./02-quickstart-and-workspaces.md) | [Index](./README.md) | [Next: Chapter 04: Rich Authoring, Embeds & Export →](./04-rich-authoring-embeds-and-export.md)

