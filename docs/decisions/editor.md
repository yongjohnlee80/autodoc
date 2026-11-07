# Editor decisions

The decisions behind AutoDoc's editor, in the terminal (`autodoc --ui`) and in the GUI
(`autodoc --gui`): the menu bar's dock, buffers, the Lua shell, moving between widgets, how open
files follow the disk, the views a file can have (HTML, CSV tables), fonts and panels, files
outside every workspace, the usage card, languages, and the documentation site built by AutoDoc's
own export. Each section says what was decided, why, and where the code is.

## The dock of open widgets

The menu bar's right end is a dock: one icon per widget that is open, or that keeps running with
its drawer closed (the terminal's shells, the agents, a plugin card's plugin). The editor's icon is
always there, first. Help is the last menu on the left, so the right end is free for the dock.

- A panel counts as running when one of its terminal tabs runs, or when its own `Alive` check says
  so. One icon per panel; tabs are not listed (the Go-to picker lists them).
- Icons are single Unicode symbols, and each icon's tooltip names its widget.
- While the dock lists more than the editor, the menu bar stays shown even when auto-hide is on, so
  what is running is always visible.

In the code: `tui/dock.go`, `tui/panels.go`, `tui/qml/main.qml`

## Dock clicks hide and show

A click on a dock icon toggles its widget. A shown panel's icon hides it and gives the keyboard
back to the editor; a hidden or closed one's goes there, the same way the Go-to picker does. The
editor's icon hides every shown panel, and its next click shows the same set again.

- Hiding is a window minimize, not a close: the panel stays mounted, its close hook does not run,
  and its state (a GlobeMap's zoom, filter and layout, a terminal's shells) is kept. Its icon is
  dimmed, and flashes briefly as it hides so the eye finds where it went.
- Jumping out of a panel hides it instead of closing it: a GlobeMap node, a Mindmap click and a
  Relations row open their document and hide the panel, so the dock brings it back as it was.
  Every such open gives the editor the keyboard.
- Toggling a hidden panel by its key shows it again.

In the code: `tui/dock.go`, `tui/panels.go`, `tui/globemap_gui.go`, `tui/mindmap_gui.go`, `tui/relations.go`

## A toggle raises a covered panel

Panels and windows can lie over one another, so an open panel isn't always seen. A panel's toggle
(its key, such as SPC x for the todos, its `:` command, Lua action, menu item and dock icon)
decides by whether the panel is seen whole, never by where the keyboard is:

- closed or hidden: shown, in front, with the keyboard;
- open but covered, even by one cell, by a window or panel stacked over it: brought to the front
  with the keyboard, and not hidden;
- open and uncovered: hidden, whether it has the keyboard or not. A panel tiled beside the editor
  hides at the first press.

Covered is golib's `OverlayHost.Covered`: a shown panel or window above it in the stack overlaps it.
An open dialog or a notification over it doesn't count. Every toggle decides by the same coverage
rule (`panelCovered`): the keys, commands, Lua actions and menu items through `togglePanel`, the
dock icon in its own handler (`goDock`).

In the code: `tui/panels.go`, `tui/dock.go`

## Multiple buffers

Several files are open at once as buffers; the editor shows one, the active buffer. The host's
"current file" points at the active buffer, so code that reads the page reads the active buffer.

- Opening asks nothing: a file already open shows its buffer, another file adds a buffer, and the
  buffer left keeps its unsaved edits. Closing a buffer, reloading it or quitting asks about each
  modified buffer in turn.
- A load, a save and its conflict carry the buffer they started for, and land in it whatever is
  active when they finish.
- A buffer that is not shown is edited through a headless editor of its own, which runs the same
  commands the user's keys run.

In the code: `tui/buffers.go`, `tui/files.go`

## Buffers in Lua and the editor socket

Scripts and agents reach every buffer, not only the shown one. `ed` and `autodoc.document()` stay
the active buffer's, so existing scripts keep working. `autodoc.buffers()` lists the buffers in
opening order, and `autodoc.buffer(n)` is an editor object for buffer `n`, shown or not. A call on
it runs as a command does: one batch, one undo step, with the same capability gate as `ed`.

Events name their buffer: `open` is a load, `enter` and `leave` a switch, `close` a close, `save` a
save, and `change` carries `{buffer, revision}`. The editor socket's verbs take an optional buffer
too, the active one by default, so an agent's command gives the result the user's would.

In the code: `script/buffers.go`, `tui/luabuffers.go`, `tui/luaevents.go`, `tui/editorsocket.go`

## Switching buffers

Each surface switches its own way. The terminal has `:b <n|name>`, with Tab completion, and a
Buffers picker that lists each buffer by number and name, `[+]` on a modified one, and previews its
text as it is, unsaved edits included. The GUI shows the buffers as tabs in the editor's title bar.

In the code: `tui/bufferpick.go`, `tui/buffers.go`, `tui/qml/main.qml`

## A Lua shell in the terminal panel

A terminal tab can run a program inside AutoDoc as well as a child process on a PTY. The first such
program is a Lua shell: `:lua` opens one (`lua`, then `lua 2`, ...), and `:lua <chunk>` runs one
line and shows its values as a notice.

- The shell's own goroutine owns the screen and the line editing (history, arrows, bracketed
  paste); the UI loop owns Lua. They meet only by messages: one chunk in flight at most, keys typed
  meanwhile held in order and applied after the answer. The loop never waits on the shell.
- Every value a chunk returns is written back; an error shows in the problem colour and the shell
  keeps running. A chunk that ends early (an open `for`, a long string) continues on the next line.
- Chunks run with the user's trust and the scripts' deadline, as `lua/*.lua` files do.
- Closing is safe from any goroutine, and a closed shell's queued chunk runs nothing. `:lua-reload`
  moves an open shell onto the new engine.

In the code: `tui/luashell.go`, `tui/terminalpanel.go`, `tui/scripting.go`

## The shell's environment

A shell runs each chunk in an environment of its own: a table whose reads fall back to the engine's
globals and whose writes stay in it, so `x = 1` in one shell is not seen by another shell or by a
loaded script. It holds its own `print`, which writes into the shell, and `ed`, the page. The engine
owns every shell's environment, so a callback made in a shell (a search's answer) still runs and
prints there later. Changing a shared table such as `autodoc` changes it for every script, under
the same trust a script file has.

In the code: `script/eval.go`, `tui/luashell.go`

## Going to a widget by name

The host lists the places the keyboard can go: the editor, every panel in registry order, and each
tab of the terminal and agent panels. The menu bar, dialogs and the status line are not targets;
the menu bar keeps its own ways in (F10, Alt+letter, `SPC m`).

- `:go <name>` goes to one. A name matches exactly first, then as the one target that contains it.
  `:go` alone and `SPC Tab` open a Go-to picker over the list.
- F6 and Shift+F6 step the keyboard through the open targets, wrapping.
- Going never closes: a closed panel opens its own way, an open one takes the keyboard, and a
  hidden one is shown again.
- Lua lists the targets with `autodoc.widgets()` and goes with `autodoc.run("widget.go", name)`,
  a plugin card's panel included.

In the code: `tui/widgets.go`, `tui/dock.go`

## Open files follow the disk

The editor watches the folder of every open buffer's file, natively where the platform can
(inotify, FSEvents) and by polling where it cannot. One watch serves all the buffers in a folder
and ends with the last of them. Each change is checked against the daemon's version of the file:

- the same version (the editor's own save, a touch): nothing;
- a newer version and no unsaved edits: the buffer reloads silently, as one undo step, the cursor
  kept;
- a newer version and unsaved edits: the buffer is badged, and asked keep / reload / overwrite
  when it is next entered or saved, never while it is typed in;
- the file deleted: the buffer stays, badged, and a save creates the file again. A todo task's
  file is the exception: see [A task's file follows its task](#a-tasks-file-follows-its-task).

This keeps the editor honest when an agent or another editor writes the files it has open.
`editor.document` reports a buffer whose file changed or was deleted on disk as `disk: "changed"`
or `disk: "deleted"`.

In the code: `tui/diskwatch.go`, `tui/buffers.go`

## A task's file follows its task

A todo store keeps each task in the folder of its status (`open/`, `in-progress/`, `deferred/`,
`completed/`, `archived/<year>/<month>/`). A status change, the assignment of an open task, the
refresh's archiving, or a move by hand moves the task's file. To the disk watch, that is the file
deleted, and the rule above would let a save write it again: a second file of the task, beside the
one that was moved.

So before a buffer whose file is gone is badged deleted, the editor asks where the task is now:

1. by the change log: the window's todo feed reads `todos.changes`, and each move it reads names
   the old path and the new one. The feed also asks for an immediate check of every buffer on a
   path a change moved, rewrote or removed, without waiting for the disk watch;
2. by its id: `todos.get` of the file's name in the store that holds it. This also finds a move
   the log reports only as a hand edit, and works in a window with no todo feed.

A move is its own store's: the feed reads several stores, and the same relative path can be a task's
in two of them, so a move is taken only for the file at its store's path joined with its old path.
And the file a move names is taken only when its own `id` is the task's (the file's name): a stale
move to another file, of another task or of none, is passed over for the lookup by id.

What happens next:

- **moved:** the buffer takes the new path, with its text, cursor and undo kept. With no unsaved
  edits it takes the moved file's text as one undo step, as for a file changed on disk. With
  unsaved edits it keeps them, badged changed on disk, and asks keep / reload / overwrite when it
  is next shown or saved. If the new path is already open in another buffer, a clean buffer closes;
  one with unsaved edits keeps them, and its save is refused.
- **removed** (`todos.get` finds no such task): a clean buffer closes. One with unsaved edits keeps
  them, badged deleted on disk, and its save is refused: a removed task is never written back from
  a buffer.
- **not a task's file**, or the daemon cannot say: the file is deleted, as before.

The daemon backs this up on its side: a client's create of a second file of a task is refused (see
[todos: A task's file is written at one path](todos.md#a-tasks-file-is-written-at-one-path)).

Why: a task must have one file. A second one, in an old bucket, is a task with two statuses, and the
store reports it as a conflict that a person must settle by hand.

In the code: `tui/todomove.go`, `tui/diskwatch.go`, `tui/files.go` (`recheckBuffer`, `writeAs`),
`tui/crewtodos.go`

## HTML files open raw or simplified

Opening an `.html` or `.htm` file asks how: Simplified, its derived text, read-only like any
derived document, or Raw, its own bytes, editable and saved through the daemon. A reload keeps the
way it was opened. The daemon reads and writes the bytes with `doc.read_raw`, `doc.write_raw`,
`file.read_raw` and `file.write_raw`; they reach only formats whose source is text, never a DOCX
or PDF, and versions and conflicts work as for any write.

A raw HTML file's Rendered view is the drawn page itself, in place of the text body: under `--gui`
the UI library's HTML view, in a terminal the note page's cell view (golib's HTMLView in cells, as
a note's page is), the file's own bytes in either, its images from where a page's come from. Ctrl+T
flips between the source and the page; the page scrolls with the usual keys, its links open as the
preview's do, and it is never edited: in a terminal its first editing key opens the source there, as
on a note's page.

In the code: `tui/files.go`, `tui/htmldoc.go`, `tui/notepage.go`, `rpc/server.go`

## The HTML preview pane

File › Preview HTML exports the note in-process and shows it in a pane beside the editor, in the
UI library's HTML view, with no JavaScript. Under `--gui` the view is drawn natively and its text
is selectable. In a terminal it is drawn in cells, as the note's Rendered page is: Mermaid diagrams
and Vega-Lite charts in cells, code in the theme's syntax colours. It needs no graphics protocol
and no browser on the host, so it works over ssh. An edit exports the note again after a moment of
quiet, and the cursor's block scrolls to the pane's top. A link clicked in it opens a note, a file
or the browser. One menu item, with the view chosen by the backend that runs.

The terminal once showed the page as an image, rendered by a headless browser, and opened the
browser where it couldn't. That needed kitty graphics and Chromium on the host, which an ssh
session rarely has, and the image had no links. The pane replaced it; View › Image previews now
governs only View › Diagram.

In the code: `tui/htmlpane.go`, `tui/htmlpane_cells.go`

## Where a page's stylesheets and images come from

A page's linked stylesheets and images load from a sandboxed root. A workspace file's root is its
workspace's folder, with paths read against the file's own folder, so a set of pages can share an
`../assets` folder inside the workspace. A file outside every workspace has its own folder as the
root, and anything above it is refused rather than fetched by widening the sandbox. AutoDoc says
so once per page; adding the folder as a workspace is how the user grants that trust.

In the code: `tui/htmldoc.go`, `tui/htmlpane.go`

## The rendered view is kept across files

The view the user chose, Raw or Rendered, stays chosen across files. A file with no Rendered view
(a `.sql` buffer, say) is drawn as text while the choice stays, and the next file that has one is
Rendered again with no key pressed.

In the code: `tui/files.go`

## CSV and TSV files as editable tables

A `.csv` or `.tsv` file's Rendered view is an editable table, in the GUI's editor and in the
terminal's, and such a file opens Rendered. Each table buffer has its own table, made when it is
first shown and kept with the buffer, over the editor's own text: an edited cell is written back to
that record's lines only, marks the buffer modified, saves with `:w` and undoes with `u`, in the
table or in Raw. Ctrl+T shows the text. Fields are kept losslessly (quotes, newlines, tabs), and an
untouched commit writes nothing.

In the code: `tui/tabledoc.go`, `tui/htmldoc.go`, `tui/files.go`

## A terminal note's Rendered view

In a terminal, a note's Rendered view is its page, as in the window: Raw is the text, Rendered the
note exported in process (as the HTML pane exports it) and laid out in cells by golib's HTMLView.
Tables are boxed, headings told apart by level, code set off, every Mermaid kind golib's parser
reads drawn in box-drawing cells, and its Vega-Lite charts drawn in cells: bars in eighths of a cell,
line, point and area charts in Braille, and a table for the rest of golib's subset. A diagram of a
kind it doesn't read, or a chart outside that subset, is a line saying it isn't drawn in the
terminal and that Raw shows its source, in the UI's language; never its source. A note opens Rendered, as a table file does. Ctrl+T flips between the
two, and the choice holds across files. Shown, the page brings the cursor's block to its top, and Raw
again puts the cursor at the block the reader scrolled the page to; when the
note changes under it, it is exported again once typing rests and keeps the reader's place. Its
links open as the pane's do, and its images load from the note's folder. It is the terminal's, in
either build (a GUI build run with `--ui` too); a window keeps its own Rendered view.

The window's Rendered Markdown is edited in place, and the page is not, so the page reads by its own
keys (the arrows, j/k, PgUp/PgDn, g/G, y, Ctrl+C), the mouse and its links. Any other key that types,
or Esc, opens the text at the reader's place and goes on to the editor as that same key, so `i` lands
in Insert mode. Space stays the leader, and chords stay the app's.

The rest answers as over the text:

- A right press opens the editor's menu.
- Ctrl+O and Ctrl+I walk the jump list. A jump's place in the note comes to the page's top.
- The breadcrumb follows the reader's place, the block at the page's top.

The page has no cursor, which changes two things:

- **SPC j** follows the first link at or after the page's top, among what is on screen. With none
  on screen, it says so.
- **Find** counts and marks what the page shows, while the text's find counts its source. Ctrl+T
  runs the find again in the new view's terms, so "k of N" can change. The reader stays put, and the
  current match is the first at or after their place.
  - A hit on the page puts the editor's cursor at its block, so Raw opens there, and gives the page
    the keyboard.
  - A term the page doesn't show but the source has (a link's target, Markdown's own syntax such as
    `- [ ]`) opens Raw at the reader's place, says so, and is found there. The same command means
    the same thing whether a user or a script runs it.
  - A term in neither view is not found, and the view stays as it is.

In the code: `tui/notepage.go`, `tui/feature.go`, `tui/find.go`, `tui/golink.go`, `tui/outline.go`

## The table's clipboard, menu and row numbers

The table shows row numbers and has a right-click menu of its commands. Copies and pastes use one
lossless quoted-TSV form, the same one the file reader parses. Whole rows yanked (`V`, `yy`) paste
as new records; anything else overwrites a block from the cursor's cell. The yank goes to the
editor's own register, so it survives buffer and view switches and Raw sees it too. AutoDoc
notifies when an edit is dropped by a change underneath it, or when a paste is cut off at the
table's edge.

In the code: `tui/tabledoc.go`

## Fonts, zoom and floating panels

GUI follow-ups that a terminal does not need:

- **Fonts and zoom.** The GUI window changes its cell font, size, prose font and zoom while it runs,
  from Options › Fonts and zoom…, applied at once and kept in the store (`gui.font.cell`,
  `gui.font.size`, `gui.font.prose`, `gui.zoom`). Ctrl+= and Ctrl+- step the zoom, Ctrl+0 resets
  it, and the page's right-click menu has the steps. A terminal draws in its own font, so none of
  this shows there.
- **The menu key.** Shift+F10, and `SPC .` from Normal mode, open the page's right-click menu at
  the cursor.
- **Floating panels.** The drawers move and resize anywhere over the page: Alt-drag moves one,
  Alt-right-drag resizes it, and Go › Arrange panel does both by keys. Where one ends is kept as
  percentages of the window (`tui.<panel>.float`), and it opens there from then on; its edge
  preference only says where it first appears. Go › Reset panel layout, or a new edge in
  Preferences, docks it at its edge again. No corner grip is drawn.

In the code: `tui/fonts.go`, `tui/contextmenu.go`, `tui/panelfloat.go`, `tui/panelsize.go`

## The terminal text size

In the GUI, the agent and terminal panels draw their terminal text at a size of their own: a share
of the window's text size, 50% to 150% in steps of 10%, 90% by default, kept in
`tui.agent.text.scale` and chosen in the Fonts dialog. Being a share, it follows zoom. The text is
drawn natively, off the window's cell grid. In the terminal build this does nothing: the host
terminal owns the font.

In the code: `tui/paneltext_gui.go`, `tui/paneltext_tui.go`, `tui/fonts.go`

## Files outside every workspace

AutoDoc opens, edits and saves any local file, not only a workspace's. File › Open file… (`SPC f`)
and File › New file… (`SPC n`) are file dialogs over the local disk; Open document… (`SPC o`) stays
the fast picker over the workspace in use.

- The daemon, not the editor, reads and writes these files, through `file.read`, `file.write` and
  `file.locate`, over the same document layer as workspace files: versions, conflicts, the size
  limit and read-only derived kinds all behave the same. A stale save asks keep / reload /
  overwrite; a second create is a conflict that leaves the file alone.
- A file inside a workspace's root opens as that workspace's file (`file.locate`), and a write to
  it goes through the workspace, so its rules and its index apply at once.
- The `file.*` verbs answer only a local peer on the unix socket. Attached to a remote AutoDoc,
  the menu items say "local files only", because a remote client would otherwise reach any path the
  daemon can read.
- An outside file has an empty workspace and an absolute path. It stays open when the workspace
  changes, and has no search, links, backlinks or frontmatter check; the status line says so.

In the code: `rpc/files.go`, `app/serve.go`, `tui/outside.go`, `tui/files.go`

## The Relations drawer

`SPC l` opens the Relations drawer: what the open document is connected to, grouped by the edge's
kind and direction (superseded by, supersedes, sources, cited by, amends, amended by, related, the
body's links and backlinks). `d` shows the second ring under each first-ring document, and the
unresolved relations and supersession cycles close the list. Enter opens a row in the editor and
hides the drawer, whose rows then follow the document opened. `SPC j` goes to the link under the
cursor and `SPC b b` back to the previous document; the editor's right-click menu has both. The
Neovim client's drawer groups relations the same way (`autodoc.relations`).

In the code: `tui/relations.go`, `lua/autodoc/relations.lua`

## Process stats

AutoDoc reports what its own processes use. One package reads a process: resident and peak memory,
CPU seconds so far, Go memory, goroutines and start time, using only the standard library; a size
a platform cannot report is 0. The daemon serves its own as `sys.stats`, and the app answers
`autodoc.app.stats()` from the same package. CPU stays cumulative seconds; a reader takes a
percentage between two samples.

In the code: `core/procstat/procstat.go`, `rpc/sys.go`

## Lua cards

`autodoc.ui.card{title, text, every, tick}` shows a script's Markdown in one docked card panel and
returns a handle: `card:set(text)` replaces it, `card:close()` ends it. The card is drawn Rendered,
so a `vega-lite` fence draws as a chart: in the GUI natively, in a terminal on the card's page, in
cells, as a note's page draws one. `autodoc.ui.charts()` says whether a card draws charts. `tick` runs every `every` ms (never faster than 250 ms) while the
panel is open, and a close and reopen never leave two tick chains. One panel shows one card: a new
card replaces the old, whose handle then does nothing, and `:lua-reload` ends it.

In the code: `tui/luacard.go`, `tui/cardpage.go`, `tui/luahost.go`

## The usage card

`:usage` is the first card, shipped as a Lua file: the daemon's and the app's memory and CPU, and
the workspace's index, refreshed each second while the card is open. Line charts of the last minute
follow the tables, in a window and in a terminal alike.

In the code: `tui/lua/usage.lua`, `tui/luacard.go`

## Six languages

AutoDoc's UI, in the terminal and the GUI, speaks English, Korean, Japanese, Simplified Chinese,
Spanish and Brazilian Portuguese, on the UI library's localization with no new mechanism. One
catalog set serves both builds. The language is one store-wide preference, `tui.language`, chosen
in Options › Language and in Preferences; a switch relabels everything in one frame, and text
AutoDoc composes in Go is composed again. A stored language this build does not offer shows as
English and the store keeps it.

- Help › Tutorial reads `docs/tutorials/<tag>/` in the UI's language, falling back to the English
  page of the same name. The Reference Book stays English.
- Tests gate it: every id used is in the English catalog, every shipped language covers every id,
  the library's checks find no wrong mnemonic or collision, and a switch relabels an open dialog.

In the code: `tui/language.go`, `tui/feature.go`, `tui/tutorial.go`, `tui/qml/i18n/`

## The documentation site

`docs/` is published as a website by AutoDoc's own HTML export, not a second renderer.
`autodoc --export=site --output _site docs` builds the sections `docs/site.toml` names, each
folder's Markdown a page in file-name order with its `README.md` as the index. Every page gets
relative links to the other pages (so the site works under any path), a sidebar, and previous and
next links. The export package only reads; the command writes the pages, into a new or empty folder
that is never inside `docs`. A pushed `v*` tag builds and deploys the site to GitHub Pages, so the
site matches the release.

In the code: `core/export/site.go`, `app/export.go`, `docs/site.toml`, `.github/workflows/pages.yml`

## Charts drawn as SVG

The site draws Vega-Lite charts as the app does: the GUI build passes the export a chart hook that
lays the chart out with the UI library's chart code and writes it as inline SVG in the export's
palette. A chart outside the supported subset is captioned with the reason. The export package
never imports the GUI; the terminal build passes no hook and leaves the spec as it is. The site
workflow therefore builds with `-tags gui`, which needs the window system's libraries but no
display.

In the code: `app/exportchart_on.go`, `app/export.go`, `.github/workflows/pages.yml`, `.github/workflows/ci.yml`
