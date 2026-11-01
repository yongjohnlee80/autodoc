# Chapter 3: Modal Editor and Panels

Both `autodoc --ui` (the terminal TUI) and `autodoc --gui` (the native desktop application) show the same thing: one **page**, 120 columns wide and centred, and **panels** that open over it from its sides. The page edits in one of two editor modes, **Vim** (modal, the default) or **Text** (modeless). Every open file is a **buffer**, and the leader key, `SPC`, reaches the rest.

---

## 3.1 The Page

The screen is a page and nothing else: the file, 120 columns wide and centred, the ruler at its
right edge. With no file open, the page is an untitled draft: type into it (`i`), and `Ctrl+S`
names and saves it as a file. Everything else comes when it is asked for:

- **The menu bar** shows at the top. Set to hide (`Options › Editor preferences`), it hides until
  `F10` or an `Alt+letter` brings it up. On a Mac, `Option+letter` is `Alt+letter` whatever the
  terminal's Option setting (on a US layout; `Option+E`, `I`, `N` and `U` are dead keys there, so
  their menus need `F10`), and `F10` may need `Fn`.
- **The status line** (`SPC t`), shown by default: the editor's mode, the workspace, and the file,
  with `[+]` while it has unsaved changes. Set to hide, it still shows while the TUI is not
  connected.

  The status line ends with semantic search's state: a green dot and "semantic search" while a
  provider answers, a red one and "lexical search" while it doesn't (none in use, a model switch
  under way, or the provider not answering). While the provider embeds, a spinner turns beside a bar
  of the sections covered. A model switch notifies when search is temporarily words-only and when
  semantic search returns; a search already open refreshes its hits without another keystroke.
- **The breadcrumb** on the page's frame follows the cursor: the headings it is under in Markdown
  (`guide.md › Setup › Linux`), the key path in YAML (`conf.yaml › server › database`). Plain text
  has none.
- **The outline** (`SPC O`, `Go › Outline…`) lists a Markdown file's headings, as the
  editor's text has them, saved or not; Enter jumps to the one under the cursor.
- **Frontmatter problems** show on a line over the page as the file is typed, once the workspace
  has a schema (`Manage… › Edit…`); they never block a save. In the GUI's Rendered View, a note's frontmatter is hidden.

**The page** wraps long lines at its width, and can number its lines in a dimmed gutter: View ›
Wrap long lines and Line numbers, or Preferences. Preferences sets the page's width too. `?` in Normal mode shows the Vim keys in a card
at the bottom right; the page keeps the keyboard, and `?` again closes it.

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
- `Ctrl+T` toggles the Rendered View (a Markdown or HTML file's in `--gui` only; a CSV or TSV file's table in `--gui` and in the terminal, §3.3);
- `Ctrl+Space` opens SPC's card;
- `Ctrl+;` opens the command line.

Search › Find in page finds in the page.

---

## 3.3 Buffers

Every file opened is a buffer, and the GUI shows them as tabs. The status line (`SPC t`) shows `[+]` while the shown one has unsaved changes.

- `H` and `L` in Normal mode show the buffer before or after the shown one, in the order of the GUI's tabs, wrapping around.
- `SPC b` opens the buffer card, whose keys act on the buffers:
  - `1` to `9` show the 1st to the 9th buffer in the tabs' order (`H` and `L`'s), and `0` the 10th. A place past the last buffer does nothing.
  - `d` (or `:bd`) closes the shown buffer, asking first over unsaved changes.
  - `r` and `l` close the buffers to the right and to the left of the shown one, nearest first, each as `d` closes it. A Stay on an unsaved one stops the rest, and leaves that buffer shown, as Close others' Stay does; otherwise the shown buffer is shown again.
  - `s` (or `:b`) switches buffer from a picker, each buffer previewed with its unsaved text. `:b <number>` or `:b <name>` goes straight to one.
  - `b` goes back to the previous document.

AutoDoc shows one page at a time: there are no window splits, and no folding. The panels (§3.5) and the GUI's HTML preview pane (`SPC H`) sit beside or over it.

### Files anywhere

`File › Open file…` (`SPC f`) opens any file on this machine's disk in a
file dialog with a preview; `File › New file…` (`Ctrl+N`, `SPC n`) makes one in any folder.
A file inside a workspace's folder opens as that workspace's file. Any other is in no
workspace: it edits, saves (a change on disk since it was read asks first), exports and
reaches the plugins, but has no search, relations or frontmatter check, and the status line
badges it `[no workspace]`. It stays open when the workspace changes. `File › Open document…`
(`SPC o`) is the fast picker over the workspace's own files.

### Files that open their own way

- **A derived document is read-only.** A DOCX or an HTML file, or a PDF where the daemon's build
  derives one (see [Builds of your own](09-code-symbol-indexing.md#5-custom-compile-time-chunkers-appoptionschunkers-and-held-files)), opens as its derived text in a
  read-only page, the frame and the status line badged `[HTML · read-only]`. Motions, find, copy and the outline
  over its headings work; edits and saves do not. `File › Open with Default App` opens the file itself in the desktop's
  viewer, for its diagrams and layout.
- **An HTML file asks how to open it.**
  - **Simplified** (`Enter`) is the derived text above, read-only.
  - **Raw** is the HTML itself, edited and saved like any file; reading it again keeps it raw.
  - In the GUI, a raw file's Rendered view (`Ctrl+T`) is its page, drawn natively.
- **A CSV or TSV file's Rendered view is an editable table**, in the GUI and in the terminal.
  `Ctrl+T` flips between it and the text, which is always there to edit. In the table:
  - `Enter` (or `i`, or typing) opens a cell, and its exact value is kept: line breaks, tabs and
    quotes. `Enter` keeps the cell, `Alt+Enter` adds a line, and `Esc` drops the edit.
  - `o` adds a row under the cursor; `dd` deletes a row; `x` empties a cell.
  - The extra row and field after the last, each marked `+`, append to the file.
  - An edit is one undo step, undone by the editor's own key (`u`, or `Ctrl+Z`), and marks the
    file modified. Only the edited record's lines change on save.
  - A file that is not valid CSV (a stray quote) opens read-only in the table; its text still
    edits.
  - A workspace opens its CSV and TSV files once its include names them (`**/*.csv`,
    `**/*.tsv`): the default include is Markdown, text and YAML.

---

## 3.4 Leader (`SPC`) Chords and Command Pickers

In Normal mode, pressing the **Spacebar (`SPC`)** opens SPC's card, listing every leader key by group. A capital is the key with Shift (`SPC O` is Shift+O). The keys, as `tui/keys.go` and AutoDoc's features bind them:

- **Find:** `SPC /` (or `SPC SPC`) searches the workspace; `SPC o` opens a document (a filter over the workspace's files, with a preview); `SPC f` opens a file anywhere; `SPC r` lists recent files; `SPC j` goes to the link under the cursor; `SPC b` the buffer card (§3.3), where `SPC b b` goes back.
- **File:** `SPC n` a new file; `SPC O` the file's outline; `SPC d` views the diagram; `SPC H` previews HTML.
- **Panels:** `SPC e` the explorer; `SPC l` the links (Relations); `` SPC ` `` the terminal; `SPC x` the todos; `SPC M` maximizes a panel; `SPC h` the notifications.
- **Agents:** `SPC g` the AI agent; `SPC G` the agent profiles; `SPC a` the agents' card, where `SPC a b` sends the page to an agent and `SPC a f` the selection or the clipboard.
- **Manage:** `SPC w` switches workspace; `SPC W` manages workspaces; `SPC s` the search models; `SPC ,` the editor preferences; `SPC k` the editor mode (Vim, Text).
- **Window:** `SPC m` shows, focuses or hides the menu; `SPC .` the right-click menu; `SPC t` the status line; `SPC L` arranges a panel; `SPC p` a plugin's commands; `SPC u` your Lua keys; and `SPC Tab`, below.
- **Help:** `SPC ?` help; `SPC A` about; `SPC Q` quit.
- **`SPC Tab` (Go to a widget)**: Opens the Go-to picker over the places the keyboard can go: the editor, every panel, and each terminal and agent tab, never the menu bar. Tab and Shift+Tab move its cursor; Enter goes there, opening a closed panel. `:go <name>` does the same from the command line (`:go explorer`, `:go terminal/lua`), and **F6 / Shift+F6** step the keyboard through the open widgets.
- **The dock (the menu bar's right end)**: one icon per widget. **✎** is the editor, and it comes first. Then comes each panel that is open, or running on with its drawer closed: a terminal with a shell, an agent, a plugin card whose plugin runs. A tab belongs to its panel's icon. A click on a shown panel hides it, not closed: it keeps everything it holds (a GlobeMap keeps its picture, view and filter), its icon is dimmed, and the keyboard goes back to the editor. A click on a hidden panel shows it again as it was, with the keyboard in it, as the Go-to picker does; a closed panel opens. **✎** hides every shown panel and remembers them; its next click shows the same ones again, unless a panel was opened, shown or closed in between. Opening a note from the GlobeMap, the Mindmap or the Links drawer hides it the same way, so the dock brings it back. A hidden panel leaves the dock only when it is closed for real. With the bar on auto-hide, it stays shown while the dock lists more than the editor, and the page moves down a row while it does.

Some keys work wherever the keyboard is: `Ctrl+h/j/k/l` move between the page and the panels (in Normal mode), `Ctrl+S` saves, `Ctrl+G` searches, `Ctrl+W` switches workspace (in the Vim mode's Insert, `Ctrl+W` deletes the word before the cursor: `Esc` first), `F1` opens the help, `F11` maximizes the terminal or the agents, and `Ctrl+Q` quits. `Ctrl+Z` never reaches a program in the terminal or the agents, so it can't stop one by accident.

### The keys at a glance

| Key | Does |
| --- | --- |
| `Space`, `Ctrl+Space` | the leader card (Space in Vim's Normal mode; Ctrl+Space in any editor mode): a key runs its command (`e`, `l`, `/`, `o`, `O`, `d`, `H`, `k`, `,`, `s` …), listed by purpose: find and open, this file, the panels, the agents, help, managing, the window |
| `Ctrl+G`, `SPC /`, `SPC SPC` | search the workspace, by words and meaning |
| `/`, `n`, `N` | find a word in the pane with the keyboard (the page, the explorer, the links), then again forward and back (Normal mode) |
| `SPC o`, `Ctrl+N`, `Ctrl+S` | open a document of the workspace, new file (any folder), save |
| `Ctrl+O`, `Ctrl+I` | back and forward along the jumps, Vim's jump list (Normal mode): links followed, documents opened, and the page's own jumps (`G`, `gg`, `%`, `{ }`, a mark's); back to another document opens it where you left it, in the view you left it in. `Ctrl+O` no longer opens a document: `SPC o` does |
| `SPC f`, `SPC l`, `SPC j` | open a file anywhere, the links, the link under the cursor |
| `SPC b` | the buffer card: `1` to `9` show the 1st to the 9th buffer in the tabs' order, `0` the 10th; `d` closes the one shown (`File › Close buffer`, `:bd`), asking first over unsaved changes; `r` and `l` close the buffers to its right and left, nearest first, each asked about as `d` asks, a Stay stopping there with that buffer shown; `s` switches buffer (`Go › Buffers…`, `:b`: every open file and draft, previewed with its unsaved text; `:b 3`, `:b name`); `b` goes back |
| `H`, `L` (`Shift+h`, `Shift+l`) | in the page, in Normal mode, the buffer before or after the shown one, in the order of the GUI's tabs, wrapping at the ends (bufferline's keys, as in autovim). This replaces Vim's `H` and `L` (the screen's top and bottom line) in Normal mode only: Visual mode keeps them, Insert mode types them, and a script's own `autodoc.map` of `H` or `L` wins |
| `SPC O`, `SPC d`, `SPC H` | the file's outline (`Go › Outline…`); the Mermaid diagram at the cursor (`File › Preview Mermaid diagram…`, or right-click `View diagram`); the HTML preview (`File › Preview HTML`) |
| `File › Open with Default App` | the file in the desktop's default app: `xdg-open`, or `open` on a Mac |
| `Ctrl+W` | switch workspace (`SPC w` too); its `Manage…` (or `Go › Manage workspaces…`) adds, edits and deletes them. In the Vim editor mode's Insert, `Ctrl+W` is Vim's: it deletes the word before the cursor (`Esc` first to switch), as Insert's `Ctrl+U` deletes back to the indent and `Ctrl+O` runs one Normal-mode command |
| `Ctrl+h` `j` `k` `l` | in Normal mode, to the open panel on that side, and back to the page |
| `SPC L` | arrange the panel with the keyboard: move and resize it (`Go › Arrange panel…`) |
| `Shift+F10`, the Menu key, `SPC .` | the page's right-click menu, at the cursor (the Menu key where the terminal sends it) |
| `Ctrl+=`, `Ctrl+-`, `Ctrl+0` | in the GUI, zoom in, out, and back to 100 % |
| `F1`, `F10`, `Ctrl+Q` | help, the menu bar, quit; `Help › Tutorial…` is the tutorial |

![Complete Modal Editor, Leader Chord, and Panel Keybinding Reference](complete-keybinding-reference.csv)

---

## 3.5 The Panels

The explorer, the links, the todos, the terminal and the AI agent each open over the page from an edge. Preferences sets the edge each first appears at, and `Escape` or the panel's key again closes it.

- **Every panel moves and resizes**: the explorer, the terminal, the agent, the HTML
  preview, and in the GUI the GlobeMap and the Mindmap.
  - `Alt` (`Option` on a Mac) with the left button drags one anywhere over the page;
    with the right button it resizes.
  - In the GUI (`--gui`), a plain drag does it too: the panel's title row moves it, and a side, the bottom or a bottom corner resizes it.
  - `SPC L` (`Go › Arrange panel…`) does both from the keyboard:
    `h` `j` `k` `l` or the arrows move it, `H` `J` `K` `L` or `Shift` with them resize it, `Enter`
    keeps it and `Esc` puts it back.
  - A panel opens where it was left, kept in the store as
    percentages of the window; its side in the preferences says only where it first appears.
  - `Go › Reset panel layout` docks every panel at its side again, as choosing a new side does for
    one. A terminal may keep `Alt`+click for itself; the keys always work.
- **Maximizing:** `SPC M` or `F11` maximizes or restores the terminal or the agents: the one with the keyboard, else the newest open. The other panels don't maximize.

### Tiled or floating: the layout

The page is a window of its own, and the explorer, the terminal, the agent, the results and the todos are each either **floating** (over the page, docked at an edge or anywhere you drag them, as above) or **tiled** (beside the page, sharing the screen with it). Each window's **Layout** says which.

- **Choosing it:** Layout › Tiled or Float is on every window's menu, right above Go to ›. A panel's Tiled is a submenu of the edges its tile docks at, Left, Right, Top and Bottom: its edge is checked while it is tiled, and choosing one docks it there, tiling a floating panel too. The page has no edge, since it fills the rest. The menu is on:
  - the page's buffer strip (`--gui`);
  - the terminal's and the agent's tabs (`≡`, or a right click on the strip);
  - the explorer's and the todos' tabs (`≡`);
  - the results table's right-click menu, at its end.

  `:layout [window]` opens it over the page, the page's when no window is named. Tab completes the windows: `editor`, `explorer`, `terminal`, `agent`, `results`, `todos`.
- **The defaults:** the page is tiled, every panel floats. A layout is kept across restarts.
- **Where a tiled panel goes:** at its tile's edge, a share of the screen across it. Layout › Tiled › Left, Right, Top or Bottom chooses the edge. These are the preferences `tui.<window>.tileEdge` and `tui.<window>.tileSize` (a percent, 5 to 90). Alt with the right button drags the border a tiled panel shares, and its new share is kept. A tiled panel's own docked place and floating rectangle stay as they were, for when it floats again.
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
5. **The AI agent (`SPC g`)**: an agent CLI in a terminal of its own, started in AutoDoc's folder for the workspace. With several agents running, `Ctrl+Tab` and `Ctrl+Shift+Tab` go to the next and the previous one's tab, as in the terminal.
6. **The notifications (`SPC h`, Go › Notifications…)**: a dialog listing what happened (a save, the connection, indexing). It is a dialog, not a panel: it has no edge and doesn't move.
7. **The results (Go › Results)**: a table of rows a plugin returns, such as a query's.
8. **Plugin cards (`SPC p`)**: a plugin's card, beside the page.
9. **The HTML preview (`SPC H`)**: under `--gui`, a pane beside the editor, the note's page drawn live as you type. In the terminal, a dialog shows the page as an image when View › Image previews is on, the terminal confirmed kitty graphics, and a headless Chromium or Chrome is there; otherwise the page opens in the browser.

The outline (`SPC O`) is a list over the page, not a panel.

### Notifications

What happens — a save, a workspace added, the connection, indexing — shows in
a corner as a toast, saying how long ago it came ("now", "15s ago"). Up to three show at once, the
newest nearest the corner; the rest wait and show in turn. A finished one stays 3 seconds (1 to 10
in Preferences), a task's progress stays until the task ends, and Preferences moves them to any
corner. Go › Notifications… (`SPC h`) lists every one, with its time. What the page says — a
find's result, the editor mode — is the status line's, not a notification.

---

## 3.6 Preferences, and `config.toml`

The editor's and the panels' settings are **preferences**. Options › Editor preferences (`SPC ,`) sets them:

- the editor mode, wrapping, line numbers, indent marks and the page's width;
- each panel's edge;
- the theme, the toast corner, and what to do with a file changed on disk.

In `--gui`, Options › Renderer sets what draws the window, at the next start ([the surfaces](01-architecture-and-surfaces.md)).

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

### The Options menu

**Options** is the menu for the editor:

- **Editor mode:** Vim (modal: `i` types, `Esc` goes back to Normal) or Text (modeless, an ordinary
  text editor's keys). `SPC k` switches between them.
- **Theme:** Dark, Light, Mono, Retro or Sepia (a reader's warm paper and brown ink).
- **Editor preferences…** (`SPC ,`): the editor mode, the page's width (applied as it is typed), the
  theme, whether the menu bar hides and the status line shows, and the side each panel opens from.
  Two more say what happens as AutoDoc starts. **Show About at startup** opens Help › About over the
  page. **Look for a new version at startup** asks GitHub, once a start, for AutoDoc's latest
  release (`api.github.com/repos/yongjohnlee80/autodoc/releases/latest`; nothing else is sent).
  When it is newer than this one, About and a notification say so and how to update: `brew upgrade
  autodoc`, `mise upgrade`, or the release's page. AutoDoc never downloads or installs anything
  itself. A build that is not a release (`dev`, or a commit after a tag) does not look. Both are on
  until you turn them off.
- **Fonts and zoom…** (the GUI's): the cell font everything on the grid is drawn in and its size,
  the terminals' font (the terminal's and the agents' shells; the cell font's unless chosen) and
  the shells' text size (a share of the cell size, 50 % to 150 % by 10 %, 90 % unless chosen),
  the prose font of the Rendered view and the HTML preview, and the zoom, 100 % to 200 %; each
  applies at once. **Zoom in**, **Zoom out** and **Actual size** are below it. A zoom or a size
  that would leave the window under 80 × 20 is refused, so the screen never needs enlarging to get
  back; a stored zoom a smaller window cannot hold is drawn at the largest step it can. A
  terminal's own settings choose its font, so there these hide.

About names the author, Yong Sung John Lee, and the license, Apache 2.0 (NOTICE).

### The System menu

**System**, right of Options, is the menu for the backend:

- **Search models…** (`SPC s`): the embedding providers and the rankers, in [Chapter 6](06-configuring-embeddings-and-rerankers.md).
- **Restart backend…** stops the daemon, and the TUI starts the autodoc installed in its place, so
  an update takes effect without quitting. The question says which version runs and which one
  starts. Indexing and embedding carry on where they stopped. A TUI built with more registrations
  than the daemon offers this once a session; see [Builds of your own](09-code-symbol-indexing.md#5-custom-compile-time-chunkers-appoptionschunkers-and-held-files).

### Walkthrough: Reviewing an Architecture Decision

To see how the editor and the panels work together in practice, consider reviewing a proposed ADR (`adrs/0048-hybrid-rrf-fusion.md`) and its linked benchmark task:

1. **Open the Document (`SPC o`)**: Press `SPC o`, type `0048 rrf`, and press `Enter` to open `adrs/0048-hybrid-rrf-fusion.md` in the center page.
2. **Survey Its Structure (`SPC O`)**: Press `SPC O` (Go › Outline…) to list the file's headings, choose `## 3. Reciprocal Rank Fusion Formula`, and press `Enter` to jump there.
3. **Inspect What Links Here (`SPC l`)**: Press `SPC l` to open the **Relations** panel: the ADR's links and its backlinks, such as the synthesis notes and reviews that link to `[[0048-hybrid-rrf-fusion]]`, and a task whose `adr:` names it.
4. **Follow a Link (`SPC j`)**: Move the cursor onto the `[[wikilink]]` naming the benchmark report and press `SPC j` to open it. `SPC b b` goes back to the ADR, where you left it.
5. **Check the Diagrams (`Ctrl+T`)**: In `autodoc --gui`, press `Ctrl+T` to switch the page into **Rendered View**, its diagrams, Vega-Lite charts, and CSV tables drawn in place. `Ctrl+T` again shows the source.
6. **Update the Linked Task (`SPC x`)**: Press `SPC x` to open the **Todos** panel, select the review task, and press `s` to choose its new status (the daemon's `todos.status` verb).

---

## 3.7 Files Changed on Disk

AutoDoc is file-canonical, so another program (`git pull`, an AI agent) may change a file you have open.

Open files follow the disk. The folder of every open file is watched (inotify, FSEvents; polled
where neither is there), so a file another program or an agent changes is picked up at once: one
with no unsaved changes is reloaded in place, one undo step, the cursor on its text; one with
unsaved changes is marked "changed on disk" (`[!]` on its tab and in the Buffers picker) and asked
about when you next open or save it, never while you type. A file deleted on disk keeps its text,
marked "deleted on disk", and a save creates it again. Preferences' **files changed on disk**
(`tui.disk_reload`) can ask first for clean files too. The TUI's own saves are never taken for a
change.

A save writes only over the version the file was opened at. If the file changed on disk since, the
TUI asks: keep editing, reload the disk's version (`u` brings your text back), or overwrite it with
yours. Opening, switching or quitting over unsaved changes asks first, too. A lost connection loses nothing: the open file stays
on the page through the reconnect, unsaved changes and cursor as they were. It is read again if it
changed on disk meanwhile and has no unsaved changes; if it is gone, closing it asks first; if its
workspace is no longer served, its unsaved text stays as the untitled draft.

### Every way out, and recovery

Every way out asks over unsaved changes, and the answers are the same everywhere: **Save** (then
quit once the file is written), **Discard** or **Stay**. That covers `File › Exit`, `Ctrl+Q`,
`SPC Q`, `:q`, the too-small screen's Quit and the GUI window's close button. When AutoDoc ends
without being asked to (`SIGTERM`, or `SIGHUP` because the terminal closed), the unsaved text is
kept in the state folder's `recovery/`. At the next start AutoDoc says so: "`<file>` had unsaved
changes when AutoDoc last closed … We kept a copy for you, just in case. Restore it?"

- **Restore** opens the file with the kept text, still unsaved. The disk's version is one undo away.
- **Discard** deletes the copy.
- **Later** asks again at the next start.

---

## 3.8 The Command Line (`:`)

`:` in the Vim mode's Normal mode opens **VIM Commands** (golib's editor prompt). Tab completes a
command and lists the candidates with their arguments; Enter runs it; Esc closes it. `Ctrl+;` opens it in either editor mode.

Every command a user can run is in one **command catalog**, by a stable id (`file.open`,
`todos.open`, `terminal.toggle`, `lua.upper`, `plugin.<name>.<id>`): AutoDoc's own, its features',
your Lua commands and the plugins'. The command line, the keys, SPC p and Lua all run them from
there, and F1 lists every one under **COMMANDS**, with its id, its `:` names, its keys and what it
does. A plugin runs one with `host.call` (protocol 3, [docs/plugins.md](../plugins.md)).

| Command | Does |
|---|---|
| `:12` | go to line 12 (refused past the last line) |
| `:open <path>`, `:e`, `:edit` | open a file: its absolute path, `~/…`, or a path in the workspace in use; spaces and all |
| `:w`, `:wq`, `:x`, `:q`, `:q!` | save; save and quit; save if changed and quit; quit (asks over unsaved changes); quit discarding them |
| `:find <term>`, `:nohl` | the page's find, as `/` is: marked, `n` and `N` go on; clear it |
| `:terminal`, `:term` | open the terminal, with the keyboard in it |
| `:todos` | the todos (SPC x) |
| `:replace <term> <with>` | each match in turn: `y` replaces, `n` skips, `q` or Esc stops; one undo for all |
| `:table <cols> <rows>` | a Markdown table (`Column1…`, `Item1.1…`). In Insert mode, Tab walks its cells and selects each one's text so typing replaces it; past the last cell, Tab adds a row |
| `:mermaid <type>` | a Mermaid diagram to start from: flowchart, sequence, class, state, er, gantt, pie, mindmap, timeline, journey |

The rest, and the long names of `:w`, `:q` and `:x`:

```text
:write, :quit, :exit    The long names of :w, :q and :x
:b [<number or name>]   Show a buffer; alone, the buffer picker
:bd                     Close the buffer
:go [<name>]            Go to a widget (:go explorer, :go terminal/lua); alone, the Go-to picker
:lua [chunk]            A Lua shell, or run Lua
:lua-reload             Reload the Lua scripts
:lua-install <name or git URL>  Install a Lua package
:agent-buffer           Hand the page to an agent, with your instruction
:agent-forward          Hand the selection, or the clipboard, to an agent, with your instruction
:usage                  What AutoDoc uses
```

Every other command has a key or a menu row, and a Lua script reaches any of them with `autodoc.run(id)`. A plugin's commands run from `SPC p`. `autodoc --commands-reference` lists every command AutoDoc is built with, by its id, with its `:` names ([`docs/commands.md`](../commands.md)). An agent AutoDoc started lists the live ones, your Lua and plugin commands included, with `autodoc --editor editor.commands`, and opens a file with `autodoc --editor editor.open '["path"]'`.

### Finding and Replacing

In Normal mode, `/` finds in the page, `n` and `N` go to the next and previous match, and `*` and `#` find the word under the cursor, forward and back. (`?` is the Vim keys' card, not a search.) `:find <term>` finds from the command line, and `:replace <term> <with>` replaces each match, asking `y` or `n` at each.

### The Terminal (`` SPC ` ``)

The terminal runs your shells in the workspace's folder: at the bottom, or wherever Preferences puts it.

- **Tabs:** each shell has a tab. `+` adds one, and a tab's menu has Go to ›, New, Close, Theme and font…, Maximize and Hide. `Ctrl+Tab` goes to the next tab and `Ctrl+Shift+Tab` the previous one, round from the last to the first, in input and Normal mode alike. The shell never gets them; `Ctrl+\` then `Ctrl+Tab` sends one to it. In a terminal, AutoDoc tells `Ctrl+Tab` from `Tab` only where the terminal does (kitty's keyboard protocol); elsewhere it arrives as `Tab` and goes to the shell.
- **Keys:** every key goes to the shell, `Esc` too. `Ctrl+T` (or `Ctrl+\ Ctrl+n`) leaves for Normal mode, where `h` `j` `k` `l`, `/` and `?` move over the shell's history, `v` selects and `y` copies. `Ctrl+T`, `i` or `Enter` goes back.
- **Closing:** `Escape` in Normal mode, or `` SPC ` `` again, hides the terminal, and its shells run on.

`autodoc --call` reaches the daemon from a shell:

```bash
# Search the workspace "kb" from the terminal
autodoc --call search.query '["kb", "rrf fusion", {"limit": 5}]'
```

---

## 3.9 Moving Your Keys (`keymap.xml`)

Every key is a command with an id, AutoDoc's (`autodoc.window.save`, `autodoc.leader.openRecent`)
and golib's widgets' (`tui.list.down`, `tui.editor.vim.normal.undo`). A `keymap.xml` beside
`config.toml` moves any of them, unbinds them, or gives one several keys; it is followed while
AutoDoc runs, and a file it can't read is refused whole, with its line, the keys staying as they
were. F1's help starts with the keys your file moves, and the cards name each by its new keys.

```sh
autodoc --keymap-reference   # the file's format, and every AutoDoc command with its keys
autodoc --commands-reference # every command, by its id, with its `:` names and arguments
autodoc --check-keymap       # check keymap.xml beside config.toml, with no UI or daemon
```

[docs/keymap.md](../keymap.md) is the reference, with every id and its default keys; the walkthrough below walks through one.

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

## 3.10 The GUI Window

```sh
autodoc --gui               # the TUI in a window of its own, attached to the daemon as --ui is
autodoc --gui kb            # in workspace kb
autodoc --gui --debug       # also record the window's input stream and the TUI's trace (see Chapter 2, §2.2)
```

`--gui` runs the same screen as `--ui` in a native window (golib's `gui`, on Gio): the same
keys, menus, dialogs and panels. Buttons, check boxes, text fields, tabs and scrollbars are
drawn natively, in the theme's colours, and a dialog dims what is behind it. The window can
be resized freely; below 80 × 20 cells it asks to be enlarged. `Options › Fonts and zoom…`
chooses the fonts it draws in, and `Ctrl+=` and `Ctrl+-` zoom everything in it together.

On macOS, holding a letter repeats it instead of opening the accent picker, so holding `j` or
`k` continues moving through the document. AutoDoc sets this preference for its GUI process at
startup; no `defaults` command is needed, and other apps and saved preferences are unaffected.

---

[← Previous: Chapter 02: Quickstart & Workspaces](./02-quickstart-and-workspaces.md) | [Index](./README.md) | [Next: Chapter 04: Rich Authoring, Embeds & Export →](./04-rich-authoring-embeds-and-export.md)

