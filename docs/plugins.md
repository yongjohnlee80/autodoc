# Writing an AutoDoc plugin

A plugin is a program. AutoDoc's TUI starts it and the two speak msgpack-RPC **notifications**, both
ways, over the plugin's stdin and stdout. Nothing is a request: where a plugin needs an answer (a
call, protocol 3), the answer is a notification of its own, matched by a number. A plugin is one of
three things:

- **a dialog** (protocol 1): AutoDoc draws a dialog over the page, the plugin fills it with frames,
  and it is sent every key but Esc, which closes it;
- **a card** (protocol 2): a docked panel of its own beside the page, that takes the keys only when
  focused;
- **a service** (protocol 2): no surface at all. It works on the note, or serves something of its own.

A protocol-2 plugin can also read the open note (the document feed) and have commands on the Plugins
menu and the SPC p card. A protocol-3 plugin can also run AutoDoc's own commands, the ones its
manifest declares (`calls`). A protocol-4 plugin can also show a table of rows in AutoDoc's own
results panel (`[table]`), and its commands carry the note they were chosen on. A protocol-5 plugin
can also say each cell's kind, so AutoDoc tells a NULL from the text `NULL`, sorts numbers as
numbers and writes JSON with its nulls, numbers and booleans. ADR 0209, ADR
1791268009, ADR 1791762474 and ADR 1792078052 are the decisions behind it.

[autodoc-tetris](https://github.com/yongjohnlee80/autodoc-tetris) is a complete protocol-1 example.
[`examples/python/echo_plugin.py`](../examples/python/echo_plugin.py) speaks the protocol with no
AutoDoc code at all.

## Installing, and the registry

Users install a plugin from **Plugins › Browse the registry…**, or from **Add from a git URL…** by its
URL or by its name in the registry. A Lua package installs the same way with `:lua-install <name or
git-url>`. The registry ([autodoc-plugin-registry](https://github.com/yongjohnlee80/autodoc-plugin-registry),
ADR 1792351195) lists plugins, Lua packages and KB types, each with the audits run on its commits.

Every install asks first. The card names the commit it would install, and says whether that exact
commit was audited ("✓ audited by <model> on <date> at this commit"), or when the entry was last
audited, or that it never was. The card's warning stays either way: an audit is a reading of one
commit, not a guarantee. To be listed, add an entry to the registry repo by pull request.

Each install or update stages its own clone, pinned to that commit: the card, **Audit…** and the
install all use it, and the installed folder is never built in. **Audit…** hands a fresh copy of the
clone to one of the user's agents, with a task file beside it (`docs/audit-prompt.md` and the
commit), and leaves a notification whose click brings the card back. The agent only reads; its
report is advice.

**Keep your plugin's state outside its folder.** An update builds the new commit in its clone, then
replaces the installed folder with it. The folder it replaced is kept in `plugins/.previous/<name>`
until the next update or its removal, so files a plugin wrote into its own folder don't carry over.

## The manifest

`plugin.toml`, at the top of the plugin's directory (and of its repository). A dialog, protocol 1:

```toml
name     = "tetris"                   # unique: lower-case letters, digits and -
title    = "AutoTetris"               # the menu entry and the dialog's title
kind     = "dialog"                   # "dialog", or "service" (protocol 2)
protocol = 1                          # the plugin protocol it speaks
command  = ["./bin/autodoc-tetris"]   # argv: a bare name on PATH, or a path from this directory

[dialog]
width      = 44                       # the columns inside the border (10 to 300; 40 when unset)
height     = "80%"                    # the rows inside the border (4 to 100; 20 when unset),
                                      # or a share of the screen's height ("10%" to "100%")
placements = ["right", "left"]        # where it is designed to sit, the first its default:
                                      # center, top, bottom, left, right, or a corner
                                      # (top-left …); the user picks among them (Manage › Place)
esc        = "hide"                   # "close" (the default), or "hide" for a plugin with its
                                      # own quit: Esc hides the dialog, the plugin runs on.
                                      # Not on a card, whose Esc closes its panel

[install]
build = ["go", "build", "-o", "bin/autodoc-tetris", "."]   # run here when it is added or updated
```

A card on the feed, protocol 2:

```toml
name     = "doc-stats"
title    = "Doc stats"
kind     = "dialog"
protocol = 2                 # it declares points of protocol 2
command  = ["autodoc-doc-stats"]

[dialog]
modal = false                # a card, a panel of its own; default true (a dialog that holds the keys)
width = 40                   # the panel's width at the right or left edge, height its height
placements = ["right", "left"]  # the panel docks at the right edge, or the left

[feed]
document = true              # the open note, as it is edited

[[commands]]
id    = "toggle"
title = "Show / hide the stats"
key   = "s"                  # its letter on the SPC p card; optional
```

A service on the feed that saves the note before it previews it, protocol 3. `start` and `calls`
are top-level keys, so they come before the first table:

```toml
name     = "html-preview"
title    = "HTML preview"
kind     = "service"
protocol = 3                 # it declares calls, protocol 3's
start    = "use"             # "use" (default) or "launch"; services only
calls    = ["file.save"]     # AutoDoc's commands it may run (host.call), by id; optional
command  = ["autodoc-html-preview"]

[feed]
document = true

[[commands]]
id    = "open"
title = "Open in the browser"
key   = "h"
```

A card that runs SQL and shows the results, protocol 4. Its table is AutoDoc's to show; the plugin
sends the rows:

```toml
name     = "sql"
title    = "SQL"
kind     = "dialog"
protocol = 4                 # it declares [table], protocol 4's
command  = ["autodoc-sql"]

[dialog]
modal = false

[feed]
document = true              # each command carries the note as it stood (protocol 4)

[table]
title = "Results"            # the results panel's heading when a table names none

[[commands]]
id    = "run"
title = "Run statement or selection"
key   = "r"
```

A manifest AutoDoc cannot run is listed in the Plugins menu disabled, with why. Among the reasons:

- an unknown kind, or a protocol outside 1 to 9;
- an unknown key, at any level (`dialog.colour`): AutoDoc decodes the manifest strictly;
- a name taken, no command;
- a placement, `esc` or height it does not know;
- a protocol-2 key under `protocol = 1` ("declares [feed]: needs protocol = 2"), `calls` under a
  protocol before 3, or `[table]` under a protocol before 4 ("declares [table]: needs protocol = 4");
- a `[table]` with no title;
- `start` on a dialog, `[dialog]` on a service, `esc` on a card ("a card's Esc closes its panel, and
  its plugin runs on");
- a service that starts on use with nothing to start it (no command, no feed);
- a command id that is not lower-case letters, digits and -, or appears twice; a command with no
  title; a key that is not one lower-case letter or digit, or is the plugin's twice;
- a call that is not one exact command id (`todos.*` is refused: no wildcards), is a plugin's
  (`plugin.…`), or is listed twice;
- `[open]` under a protocol before 9; an `[open]` whose command is none of its `[[commands]]`, that
  names no extension, or that names one AutoDoc reads itself (`.md`, `.txt`, `.yaml`, `.csv`, `.html`,
  `.pdf`, …) or that is not a dot and 1 to 16 letters, digits, `_`, `+` or `-`.

Adding a plugin from a git URL asks first. The question shows what its build runs, what it starts and
the risk, then what it declares: "a card beside the page · reads the open note's text (feed) ·
commands: Show / hide the stats (SPC p s)", the commands of AutoDoc's it may run: "may run
AutoDoc's commands: file.save", a table it shows: "shows a table of results (Results)", and the
files it opens: "opens .ipynb files".

## Versions

AutoDoc speaks plugin protocols **1 to 10**, and speaks to each plugin the one its manifest says. A
plugin of an earlier protocol runs as it always has, and is sent what it always was.

- **A manifest that declares any protocol-2 key says `protocol = 2`.** Those keys are
  `kind = "service"`, `start`, `[dialog] modal`, `[feed]` and `[[commands]]`. An AutoDoc that speaks
  only 1 lists such a plugin disabled, "protocol 2; this AutoDoc speaks 1", so a card never runs there
  as a modal dialog without its feed.
- **A manifest that declares `calls` says `protocol = 3`.** An AutoDoc that speaks only 1 and 2
  lists it disabled ("protocol 3; this AutoDoc speaks 1 to 2"), as it does a protocol-2 manifest on
  an AutoDoc of 1.
- **A manifest that declares `[table]` says `protocol = 4`.** An AutoDoc of 1 to 3 lists it disabled
  ("protocol 4; this AutoDoc speaks 1 to 3"), so a plugin never runs where its results cannot show.
  Protocol 4 also changes what a plugin on the feed is sent: its commands carry the note, and the
  note carries byte offsets (below). A protocol-2 or -3 plugin is sent neither.
- **A plugin that sends its cells' kinds says `protocol = 5`.** Kinds are read only from a plugin
  whose manifest says 5; an AutoDoc of 1 to 4 lists it disabled ("protocol 5; this AutoDoc speaks 1
  to 4"), so a plugin that tells NULL from `NULL` never runs where the two would be confused.
  Nothing else changes: no message is added, and the host fetches what it fetched at 4.
- **A card or dialog that handles pointer input says `protocol = 6`.** AutoDoc sends left presses
  and wheel steps only to a protocol-6 plugin, with coordinates inside its displayed frame. An
  older AutoDoc refuses the manifest rather than showing a card that cannot receive its pointer.
- **A plugin that keeps a session, reads why it closes, or takes a command's arguments says
  `protocol = 7`.** AutoDoc hands it its session slot in `plugin.open`, tells it the reason in
  `plugin.close`, and sends `plugin.command` with `args` (below). An older AutoDoc refuses the
  manifest, so a plugin never runs where its sign-in would be asked again at every start.
- **A plugin that posts notifications says `protocol = 8`.** It may send `host.notify` (below).
  An older AutoDoc refuses the manifest, so a plugin never reports into a host that would drop its
  messages without a word.
- **A plugin that opens a kind of file says `protocol = 9`.** Its `[open]` names the extensions it
  opens and the command it opens them with (below). Nothing on the wire changes. An older AutoDoc
  refuses the manifest, so a file the plugin would translate never opens as raw text instead.
- **A card or dialog that draws pixels says `protocol = 10`.** Its `[surface]` asks for a pixel
  surface (below): frames over a side channel, and the pointer in dp. An older AutoDoc refuses the
  manifest, so a plugin never runs where its frames would have nowhere to go.
- **The protocol rises** when a manifest can declare something an older AutoDoc must not run
  without, when a meaning changes, or when a plugin must handle something new. Everything a
  protocol adds is declared in the manifest, so a plugin is never sent what it did not ask for.
- The Go SDK speaks 1 to 10, and answers `plugin.open` with the protocol it was opened at.

## The run

- The command starts in the plugin's directory, in a process group of its own. Its environment adds:
  - `AUTODOC_PLUGIN_PROTOCOL` (the manifest's protocol);
  - `AUTODOC_PLUGIN_DIR`;
  - `AUTODOC_SOCKET`, the daemon's socket, for a plugin that uses AutoDoc's API as any client does
    (`AGENTS.md`);
  - `AUTODOC_PLUGIN_SURFACE_FD=3`, for a pixel surface's side channel, when the plugin has one
    (protocol 10, below).
- **stdout is the protocol's.** Write anything else to stderr, which goes to the plugin's log.
- **When it starts.**
  - A dialog or a card starts from its Plugins menu entry, or from one of its commands.
  - A service with `start = "use"` starts on its first command, or on its feed's first document.
    One with `start = "launch"` starts with the TUI.
- The close is bounded. It starts on Esc (a dialog's), on the plugin's `host.close` (a dialog's or a
  service's), on its menu's Turn off, on an update or a removal, on a fault, or when the TUI quits:
  1. AutoDoc sends `plugin.close`;
  2. after 2 s it sends the group SIGTERM;
  3. after 1 s more, SIGKILL.
- A plugin that exits unasked, does not answer `plugin.open` within 5 s, or answers with a protocol
  its manifest does not say is closed, and a toast gives the last line of its stderr. A failed
  service is not started again until its next use.

## The protocol

Each notification carries one map.

| From | Notification | Since | Its map |
|---|---|---|---|
| AutoDoc | `plugin.open` | 1 | `{protocol, width, height, theme}`: the first, once. A service's has no `width` or `height`; from 7, `{session}` when its slot holds one |
| AutoDoc | `plugin.key` | 1 | `{key, text, ctrl, alt, shift}` |
| AutoDoc | `plugin.resize` | 1 | `{width, height}`: the dialog laid out at another size |
| AutoDoc | `plugin.theme` | 1 | `{theme}`: the theme switched |
| AutoDoc | `plugin.hide`, `plugin.show` | 1 | `{}`: under `esc = "hide"`, Esc hid the dialog, and its menu entry showed it again; a card's panel closed, and opened again |
| AutoDoc | `plugin.focus` | 2 | `{focused}`: a card took the keys, or gave them back |
| AutoDoc | `plugin.document` | 2 | `{path, workspace, text, cursor, selection, version, too_large}`: the open note, under `[feed] document = true`; from 4 also `{cursor_byte, selection_bytes}`; `root` (the workspace's absolute folder) whenever the note is a workspace's |
| AutoDoc | `plugin.command` | 2 | `{id}`: one of the manifest's `[[commands]]`, chosen; from 4, under the feed, `{id, document}`; from 7, `{args}` too when it has arguments |
| AutoDoc | `plugin.result` | 3 | `{call, ok, error}`: the answer to the `host.call` numbered `call` |
| AutoDoc | `plugin.table.fetch` | 4 | `{id, n}`: up to `n` more rows of the table `id` |
| AutoDoc | `plugin.table.close` | 4 | `{id}`: the table is gone: its panel closed, or a newer table replaced it |
| AutoDoc | `plugin.pointer` | 6 | `{kind, button, x, y, count}`: an unmodified left press (`press`, `left`) or wheel step (`wheel`, `up`/`down`) inside the card or dialog; frame-local nonnegative coordinates and count 1–16 |
| AutoDoc | `plugin.close` | 1 | `{}`: the last; from 7, `{reason}` (below) |
| plugin | `host.ready` | 1 | `{protocol}`: the answer to `plugin.open`, with the protocol it was opened at |
| plugin | `host.frame` | 1 | `{rows}`: the whole dialog, at any time. A service's is dropped |
| plugin | `host.title` | 1 | `{title}` |
| plugin | `host.call` | 3 | `{call, id, args}`: run AutoDoc's command `id`, one the manifest's `calls` declares, with `args` (strings) |
| plugin | `host.table` | 4 | `{id, title, fields, rows, more}`: a table for the results panel, under `[table]`; from 5 also `{kinds}` |
| plugin | `host.rows` | 4 | `{id, rows, more}`: the table's next rows, answering `plugin.table.fetch`; from 5 also `{kinds}` |
| plugin | `host.table_end` | 4 | `{id, error}`: the table's rows ended before the last; `error`, when set, says why |
| plugin | `host.session` | 7 | `{values}`: the plugin's session slot replaced, a map of strings (below) |
| plugin | `host.notify` | 8 | `{message, id, ongoing}`: a notification; with an id, a toast updated in place while `ongoing`, ended by one without it (below) |
| plugin | `host.close` | 1 | `{}`: asks to close; a card asks its panel to close, and runs on |

- **Keys.** `key` is a printable key's text (`" "` for Space), or one of `Up`, `Down`, `Left`,
  `Right`, `Enter`, `Tab`, `Backspace`, `Delete`, `Insert`, `Home`, `End`, `PageUp`, `PageDown`, `F1`
  to `F12`. A chord with no text (Ctrl+b) sends its letter with `ctrl` set.
- **Frames.** `rows` is a list of rows, each a list of runs `{t, fg, bg, b, i, u}`: text, colours, and
  bold, italic and underline.
  - Colours are the themes' vocabulary: `"default"` (the dialog's own), an ANSI name (`"red"`,
    `"brightwhite"`, `"gray"`), or `"#rrggbb"`.
  - Past the dialog's size a frame is clipped.
  - AutoDoc paints only the latest frame, so send as often as you like.
- **The theme** is `{name, colors}`: the theme's colours by dotted name (`"document.cursor"`,
  `"app.window"`), for a plugin that wears the theme.

### Cards (`[dialog] modal = false`)

- **A card is a docked panel of its own**, as AutoDoc's results panel is. It opens at the edge of
  its first placement (`right`, `left`, `top`, `bottom` or `center`; a corner at its side's edge,
  `top-right` at the right), or the one the user chose (Manage › Place), as wide (at a side) or as
  tall (at the top or bottom) as `[dialog] width` and `height` ask, until the user sizes it. It
  moves and resizes as every panel does (Alt/Option-left drag, Alt/Option-right drag,
  Go › Arrange panel), and AutoDoc keeps where it is left and its size. The plugin is told each new
  size (`plugin.resize`).
- It takes the keys only when it is focused. A click on it focuses it, and so do its commands. While
  focused it is sent every key but Esc. The plugin is told `plugin.focus {focused: true}` when it
  takes them, and `{focused: false}` when it gives them back.
- **Esc closes the panel, and the plugin runs on.** So does Close (Esc) on its right-click menu. The
  keys go back to the editor, and the plugin is told `plugin.hide`: its tables, its connection and
  whatever else it holds stay as they are. A command, its Plugins menu entry or its `toggle` opens
  the panel again, and the plugin is told `plugin.show`.
- **q is the plugin's.** AutoDoc never takes a key from a focused card: a card with text fields
  types q into them. A card that wants q to close it sends `host.close` when it is not typing,
  which closes its panel as Esc does: the plugin is told `plugin.hide` and runs on. A card's
  `host.close` never stops it.
- It stops when the user turns it off: its Plugins menu's Turn off, or Manage plugins. Its `toggle`
  while its panel is open (below) only hides it. It stops too when it exits, or when AutoDoc quits.
- A card's panel is made when AutoDoc starts: a card plugin added while it runs has its panel from
  the next start. Until then AutoDoc says so itself: once as the card is found ("<title> is
  installed: its card appears after AutoDoc restarts"), on its Plugins menu row, and again when it is
  opened or one of its commands is run. A plugin's own guide need not explain it.
- **A card, or a dialog, draws no key help of its own.** It registers its commands (`[[commands]]`,
  each with an optional `key`), and SPC p and Plugins › Commands… list them with their letters. Its
  frames are its content: a line of keys there says again what AutoDoc already shows.

### The document feed (`[feed] document = true`)

- **What is sent.** The note in the editor as it stands, saved or not:
  - `path`, relative to the workspace's root, `""` for a draft not yet saved;
  - `workspace`, the workspace's name;
  - `text`, the whole text;
  - `cursor`, `{line, col}`;
  - `selection`, a list of `{start, end}`, empty when nothing is selected. `end` is just after the
    selection's last character, and a line-wise selection runs to the end of its last line.
- **Lines and columns count from 1.** A column counts characters as the editor's cursor does:
  grapheme clusters (what shows as one character), not bytes or code points.
- **When it is sent.**
  - When the plugin starts.
  - 300 ms after editing, moving the cursor or the selection, or opening a note, has paused: one
    document per pause, not one per key.
- **`version`** rises with every edit and with every note opened or closed, never with a cursor
  move. A plugin can drop an answer it computed for an older version.
- **Too large.** A document whose notification would be larger than the link's limit (1 MiB, the
  whole notification as encoded) is sent as `{path, workspace, version, too_large: true}`, with no
  text, cursor or selection.
- **Only the newest waits.** A document is state, not an event. If the plugin has not read the last
  one when a newer one is sent, the older one is dropped. Keys, commands and everything else are
  never dropped or reordered.
- **Byte offsets (protocol 4).** The document also carries `cursor_byte`, the cursor as a byte offset
  into `text`, and `selection_bytes`, each selection as `[start, end)` byte offsets. AutoDoc counts
  them from the same grapheme clusters as `cursor` and `selection`, so a plugin that slices `text`
  (the SQL statement under the cursor, say) never counts clusters itself.
- **A command carries its note (protocol 4).** The feed waits for a pause, and a newer document
  replaces a waiting one, so a document sent beside a command could arrive after it, or not at all.
  A protocol-4 plugin on the feed is therefore sent each command with the note as it stood when the
  command was chosen: `plugin.command {id, document}`, the document in `plugin.document`'s form. An
  edit, a cursor move or a selection followed at once by the command is what the command runs on.
  A feed document that comes later replaces the plugin's copy, after the command has run. The
  command and its document are sized together against the link's limit: one too large goes with
  the document's too-large form (`{path, workspace, version, too_large: true}`), so the plugin
  can say the note is too large rather than run on an old copy.

### Tables (`[table]`, protocol 4)

A plugin with rows to show (a query's result, say) does not draw them: AutoDoc shows them in its
results panel, docked at the bottom, a table with a cursor on cells, its columns scrolled and the
loaded rows sortable. The plugin sends the rows; AutoDoc asks for more as the user reads down.

- **A table.** `host.table {id, title, fields, rows, more}`: `id` is the plugin's own name for it
  (a cursor's, say), new for each table: a query run again is a new table with a new id. A
  `host.table` that reuses the id of the table shown is refused and written in the plugin's log
  ("host.table reused the shown table's id …; a new table needs a new id"): a fetch for the shown
  table may still be on its way, and its rows would join the new ones. `fields` the columns' titles, `rows` the first rows (each a list of strings, a
  cell per field), and `more` whether rows remain. `title` heads the panel; when it is empty the
  manifest's `[table] title` does. The panel opens if it is closed, leaving the keyboard where it is.
- **Paging.** As the table's cursor nears the end of the loaded rows, AutoDoc sends
  `plugin.table.fetch {id, n}`, and sends no other fetch until the plugin answers it:
  `host.rows {id, rows, more}` with up to `n` rows (fewer is fine), or `host.table_end {id, error}`
  when the rows end early (a failed fetch, a cancelled query). `more: false` is the last page.
  `host.rows` that no fetch asked for are dropped, and noted in the plugin's log.
- **At most 100 000 rows are shown.** At that cap the table stops asking for more, and its heading
  says the first rows are shown and the result has more, so AutoDoc never holds a whole large
  result. A narrower result is the query's to return: a LIMIT, a WHERE.
- **One table at a time.** A newer `host.table`, from any plugin, replaces the one shown, and the
  plugin whose table it replaced is sent `plugin.table.close {id}`. Closing the panel sends it too.
  Free what serves the rows then. Rows for a table no longer shown are dropped.
- **Each cell's kind (protocol 5).** `host.table` and `host.rows` may carry `kinds`: one string per
  row, in the rows' order, of exactly one byte per field, each one of
  - `t` text, `z` NULL (the cell's text is what shows, the plugin's `NULL` say), `n` a number (the
    text is its literal, every digit), `b` a boolean (`true` or `false`), `j` a JSON value.

  A kinds string of another length, or with another byte, makes its message malformed: a
  `host.table` is refused, and a `host.rows` page is dropped and the table's rows end there, noted in
  the plugin's log and a notice, since the rows after it would not follow those before. A table's
  kinds are decided by its `host.table`: one sent with `kinds` (an empty list when it has no rows
  yet) keeps them on every page, a page without them read as text; one sent without them reads every
  cell as text. With kinds the panel sorts NULLs last in both directions, numbers as exact decimals
  and false before true, and the JSON export writes `null`, numbers, booleans and JSON values. Without
  them every cell is text, sorted as numbers where both cells read as numbers. In Go,
  `TableOpen.Kinds` and `Peer.RowsKinds`, with `plugin.KindText` and its siblings; a host opened at a
  protocol before 5 is sent neither.
- **Keep each message under the link's limit** (1 MiB, the whole notification as encoded): size a
  page by its encoded bytes, not only its rows. `plugin.EncodedSize` measures one.
- **The panel acts on the table in the view.** Its sort, its copies, its Markdown and its exports
  take the rows loaded, in the order shown, and none of them fetches a row: **an export writes the
  rows loaded in the view; scroll to load more, or change the query.** More rows load as the user
  scrolls, and the view then holds them.
  - **The table.** Its rows are numbered. `h j k l` move (with counts: `5j`), `w` and `b` by field,
    `gg` and `G` to the first and last row; `v` selects cells in reading order, `V` rows, Ctrl+V a
    block; a drag selects cells, over the numbers rows, over the headers columns. `y` copies the
    selection, `s` sorts by the cursor's field. Its cursor dims while the keys are elsewhere.
  - **The bar** above it is a clause, chosen in its dropdown, and its text; each clause keeps its
    own text, and Enter applies it. Its ORDER BY sorts the loaded rows as SQL writes a sort: `column [ASC|DESC], …`,
    a column by its name, its `"quoted name"` or its 1-based position (a name two columns share is
    written as its position). Enter sorts; text that does not parse leaves the rows as they were
    and says why. While a name is typed its line offers the columns it may be.
  - **WHERE** filters the loaded rows, and never sends SQL or fetches a row: `column op literal`
    (`= != <> < <= > >=`), `column IS [NOT] NULL`, `column [NOT] LIKE 'pattern'` (`%`, `_`, `\`
    escapes), joined by AND, OR, NOT and parentheses; a column by its name, its `"quoted name"`, or
    `#position`. Literals are numbers, `'strings'` (`''` inside), TRUE and FALSE. It is SQL's
    three-valued logic: a row is kept only when the WHERE is true of it.
    - From a protocol-5 plugin each cell compares by its kind: a NULL only matches IS NULL, a number
      compares as an exact decimal, a boolean against TRUE or FALSE; anything against a 'string'
      compares its text. Without kinds no cell is NULL, so IS NULL matches nothing, and a cell
      whose text is a number compares as one.
    - The view is filtered, then sorted. The row numbers, the selection, the copies, the Markdown
      and the exports take that view. A WHERE that keeps no row shows the empty table: nothing to
      copy, insert or export.
  - **The line** beside the bar is the chosen clause's error, else the columns a word typed may be,
    else what the view holds: "N of M loaded rows" while a WHERE is on, and that the result has
    more while rows remain unloaded.
  - **The right-click menu** runs the results commands: Copy (TSV), Copy as CSV, JSON or Markdown,
    Select all, Sort ascending or descending by the column pressed on, Unsort, Export loaded rows…
    and Insert as Markdown.
- **What leaves the table** is AutoDoc's, its commands each saying which rows it takes:
  - `results.copy`, Copy selection: the selected cells, or the cursor's, as tab-separated text;
    `results.copy_csv`, `results.copy_json` and `results.copy_markdown` the same as CSV (the
    columns as a header), JSON (an object per row) or a Markdown table. A copy past 16 MiB of
    text copies nothing and says to export instead;
  - `results.export`, Export loaded rows…: a dialog that says what it takes ("Export N loaded rows,
    as shown", or under a WHERE "Export N rows, as shown (filtered from M loaded)", and while the
    result has more, that changing the query changes what is returned)
    and its format, CSV or JSON, then a save dialog. AutoDoc writes the rows to `<file>.part` beside
    the file, renamed over it once the last is written; a write that fails removes the `.part` and
    leaves the file as it was. Its notice says when the result has more. `results.csv` opens it on
    CSV;
  - JSON is an array of objects keyed by column, a name that appears twice taking a suffix (`id`,
    `id_2`). From a protocol-5 plugin each value follows its kind: NULL is `null`, a number its
    literal (never read through a float), a boolean `true` or `false`, a JSON value itself; a number
    or JSON that does not read as one (NaN), and text, are strings. Without kinds every value is a
    string;
  - `results.markdown`, Insert loaded rows as a Markdown table: the rows loaded now, as the table
    shows them, at the page's cursor. While the result has more it says so;
  - `results.select_all`, `results.sort_asc`, `results.sort_desc` and `results.unsort` (each
    taking a 1-based column, the cursor's when none) select and sort the loaded rows.

### Sessions, close reasons and arguments (protocol 7)

- **The session slot.** A plugin keeps what must outlive its process, a sign-in's token say, in
  AutoDoc's keeping: `host.session {values}` replaces its slot, and every later `plugin.open` in this
  run of AutoDoc carries it back as `session`. So a plugin turned off and on, updated, or restarted
  after a crash reads its token back and checks it, instead of asking again.
  - It lives in AutoDoc's memory, never on disk, and goes when AutoDoc quits or the plugin is
    removed. Nothing logs its values; a refused slot's reason is logged, never a value.
  - At most 32 keys, 8 KiB a value and 64 KiB in all; a slot over a bound is refused and the old
    one kept. A slot sent once the plugin began to stop is dropped: set it when you sign in, not in
    your close.
- **Why it closes.** `plugin.close {reason}`: `quit` (AutoDoc is quitting: the slot goes with it,
  log out now), `off` (turned off: the slot stays), `update` (its new build starts next), `remove`
  (the slot is dropped), `failed` (stopped for a fault) or `closed` (Esc on a modal dialog, or its
  own `host.close`). It is best effort: a plugin that never answered, blocks, or has died is stopped
  by the signals without one.
- **Arguments.** `plugin.command {id, args}` carries a command's arguments, from the plugin's own
  Lua package (`autodoc.plugin.run`, below).

### Notifications (protocol 8)

- **`host.notify {message}`** shows a toast in AutoDoc's corner and keeps it in the notification
  history (SPC h), as AutoDoc's own notices are.
- **A long task: `{message, id, ongoing: true}`** shows a toast that stays and is updated in place
  by each notice with the same id. **`{message, id}`**, without `ongoing`, ends it: it lingers, then
  goes, and the history keeps that last message. An id posted `ongoing` again after its end starts
  again, a new toast.
- **Ids are the plugin's own.** AutoDoc names them for the plugin, so two plugins with the same id,
  or AutoDoc's own notices, never touch each other's.
- **A stopped plugin's tasks end.** When a plugin stops (turned off, updated, removed, quitting,
  crashed), each id it left ongoing is ended with "<Title>: stopped". A card hidden is not stopped:
  its tasks run on. A notice sent once the plugin began to stop is dropped.
- **Bounds.** A message is 1 to 1024 bytes of UTF-8; an id at most 64 of letters, digits, `.`, `_`
  and `-`; `ongoing` needs an id; at most 16 ids ongoing at once. A notice past a bound, or of the
  wrong types, is refused: nothing shows, and the plugin's log says which rule, never the message.

### A plugin's Lua package (`lua/`)

A plugin may ship a Lua package in its folder, `lua/package.toml` and `lua/init.lua`, as a package
in your own `lua/` folder is made (docs/lua/reference.md). AutoDoc loads it after yours, under the
name `plugin:<name>`:

- `package.toml`'s `name` is the plugin's. Its `requires` are asked once, the prompt naming the
  plugin ("let the plugin autodb's Lua package use …"), and its `require` reads only its own folder.
- Your own package of the same name replaces it: the plugin's never runs, and a notice says so.
- `autodoc.plugin.run(id, args…)` runs one of its own plugin's `[[commands]]`, starting the plugin
  when it is not running; it answers `ok, err`. It reaches only the plugin whose package calls it:
  any other script is refused, and an id the manifest does not declare is `unknown`. Arguments need
  `protocol = 7`.
- It comes and goes with the plugin: an install, an update or a removal reloads the scripts.

```lua
-- lua/init.lua: :autodb query select 1
autodoc.command{ name = "autodb", run = function(ed, args)
  local ok, err = autodoc.plugin.run(args[1] or "run", select(2, unpack(args)))
  if not ok then error(err, 0) end
end }
```

### Services (`kind = "service"`)

A service has no surface: `plugin.open` has no size, and its frames and titles are dropped. It runs
until the TUI quits, or until it sends `host.close`. It may serve something of its own (a preview on
localhost, say) and use AutoDoc's API through `AUTODOC_SOCKET`.

### Commands (`[[commands]]`)

- Each command is on the Plugins menu, under the plugin's own entry, beside Open (a dialog's or a
  card's) and Close (a card's Turn off).
- **SPC p** (or Plugins › Commands…) chooses a plugin, then its card lists that plugin's commands
  with their letters: SPC p, the plugin, then the letter. AutoDoc's own leader letters are never a
  plugin's: SPC p is a card of its own.
- **Letters.** A letter is unique within its plugin only: two plugins may both use `s`, each on its
  own card. `q` closes the card, so it is never a command's. A command whose letter is already taken
  on its card (`q`, or a preference that gives it another command's letter) is listed unbound,
  saying whose the letter is: the command earlier in the manifest keeps it. The preference
  `tui.plugin.<name>.key.<id>` overrides a manifest's letter, and `""` unbinds the command. Set it
  with the daemon's `preference.set`, then restart the TUI.
- **What a command does.**
  - It starts its plugin first when it is not running, and is sent once the plugin has opened.
  - A card's commands open its panel and focus it.
  - A card's command `toggle` is AutoDoc's own and is never sent: it starts the card, its panel open
    and focused; it opens the panel of a card that runs hidden, focused; and it closes the panel of
    a card that is shown, the plugin running on (Turn off stops it).

### Opening files (`[open]`, protocol 9)

```text
[open]
extensions = [".ipynb"]   # the files it opens
command    = "import"     # one of its [[commands]]: run with the file's absolute path
```

- **A file with one of the extensions is handed to the plugin, not opened.** However it is opened
  (the open picker, File › Open file…, the explorer, a link, recent files, back and forward, a
  plugin's `file.open_path`), AutoDoc runs the plugin's `command` with one argument: the file's
  absolute path (`plugin.command {id, args}`, as protocol 7 sends arguments). It starts the plugin
  first when it is not running, and shows a card's panel, as any of its commands does.
- **The plugin then does what opening means for it,** say, translate the file into a note and open
  that through `host.call("file.open_path", path)` (listed in its `calls`).
- **A plugin never takes AutoDoc's own extensions:** `.md`, `.txt`, `.yaml`, `.yml`, `.mmd`, `.csv`,
  `.tsv` and the derived formats (`.html`, `.pdf`, `.docx`, …). So a note a plugin writes and opens
  never comes back to it.
- **Two plugins claiming one extension:** the first in the Plugins list opens it. Manage plugins… shows the other's row with `opens .ipynb: shadowed by <name>`.
- Creating a file (File › New file…, a draft's first save) opens it, and is not handed to a plugin.

### Pixel surfaces (`[surface]`, protocol 10)

A card or dialog can draw pixels instead of cells: a picture of its own, a canvas, a web page
(ADR 1792797354). In AutoDoc's GUI the surface fills the card's area; in the terminal the plugin
draws cells, as any plugin does.

```text
protocol = 10

[surface]
required     = false     # true: a host without pixels (the terminal) doesn't open the plugin
context_menu = "host"    # "host": a right press opens the card's menu; "plugin": it is the plugin's press
text_input   = true      # the surface takes keys and IME text, and reports a caret
```

- **The side channel.** Frames don't travel on stdout. The plugin gets a Unix socket as fd 3,
  named in `AUTODOC_PLUGIN_SURFACE_FD`, which carries buffer handles. Set close-on-exec on it
  before starting any process of your own (the Go SDK does), so a child never inherits it.
- **Generations.** After `plugin.open`, `plugin.surface {gen, w, h, scale, kinds}` gives the
  surface's size in dp, the device scale, and the frame kinds the window draws. The side channel
  brings the same generation's buffers: shared memory the host allocates, which the plugin maps
  read-write. A resize, a scale change or new caps is a new generation. Match the two halves by
  `gen`, acknowledge it (`surface.ack`), and draw only for it.
- **Frames.** Draw into a buffer you have credit for, and send `frame {seq, gen, buffer}` with its
  pixels complete: there is no fence. The host answers each frame's outcome (`frame.done`:
  drawn, superseded, not_shown, unsupported, closed or retired), then the buffer's credit
  (`buffer.free`), in that order. A frame of a retired generation, in transit across a resize, is
  answered `retired`.
- **Input.** The pointer comes as `plugin.surface.pointer {kind, button, x, y, dx, dy, count,
  ctrl, alt, shift, super, t}`, in frame-local dp: move, press and release of left, middle and
  right, scroll and leave. A scroll's `dx` and `dy` are its precise distance in dp, the part a
  touchpad scrolls between wheel steps included. The Go SDK hands a busy handler consecutive
  moves coalesced to the latest, and scrolls summed. Keys are `plugin.key`, as ever.
- **IME.** The host keeps the composition: it draws the preedit at the caret the plugin reports
  (`host.surface.caret {x, y, w, h, baseline}`, or `{}` for none) and sends the committed text as
  `plugin.key`. With `text_input = false`, keys still come, but there is no caret and no IME text.
- **The clipboard.** `host.clipboard.write {text}` writes it. A plugin reads it only by the user's
  paste, `plugin.paste {text}`.
- **The pointer's shape.** `host.surface.cursor {shape}` sets the pointer over the surface, until
  the next one: `default`, `text`, `hand`, `crosshair`, `move`, `resize-ew`, `resize-ns` or
  `not-allowed`. A name the host doesn't know is the default.
- **What closes the plugin.** A malformed side-channel message, a frame for a buffer it has no
  credit for, a repeated `seq`, or a side channel it stops reading. The close is the usual one.
- **Today:** Linux, with shared memory; macOS and plugin-exported DMA-BUFs follow.

In Go, a `Handler` that implements `plugin.SurfaceHandler` gets the surface:

```go
func (s *spike) Surface(sf *plugin.Surface, info plugin.SurfaceInfo) {
	go func() {
		for {
			b, err := sf.Buffer(ctx) // a credited buffer of the current generation
			if err != nil {
				return
			}
			draw(b.RGBA())
			sf.Present(b, image.Rectangle{}, lastInputT)
		}
	}()
}
func (s *spike) SurfacePointer(p plugin.SurfacePointer) { /* p.X, p.Y in dp */ }
```

[`examples/surface-go`](../examples/surface-go) is a complete example: an animated test pattern
with every input event drawn over it, and its frame rate and latency in its log.

### Calls (`calls = [...]`, protocol 3)

A plugin runs AutoDoc's own commands by their ids: those of AutoDoc's command catalog, which F1
lists under COMMANDS with their ids (`file.save`, `todos.open`, `search.open`), and your Lua
commands (`lua.<name>`). It may run only the ids its manifest's `calls` lists, so what a plugin can
do in AutoDoc is there to read before it is installed.

- **A call is two notifications.** The plugin sends `host.call {call, id, args}`, `call` a positive
  whole number of its own choosing that no call still waiting has. AutoDoc answers
  `plugin.result {call, ok, error}` with the same number.
- **AutoDoc answers every call it accepts exactly once**, after running it on its UI loop as a key
  runs a command. `ok` is true when the command ran; otherwise `error` is a code, a space and why:
  - `refused`: the id is not in `calls`, is a plugin's command (`plugin.…`, never another plugin's
    to run), or the call is malformed; `refused busy` when 64 of the plugin's calls are waiting;
  - `unknown`: no command has the id (a Lua command not loaded, say);
  - `failed`: the command ran and failed; why is its own error.
- **A call that reuses a number still waiting is a protocol violation.** AutoDoc writes it in the
  plugin's log, runs nothing and answers nothing, so the waiting call's answer is the only one with
  that number. A call from a plugin before protocol 3, or before it has answered `plugin.open`, is
  dropped the same way.
- A Lua command run by a call edits the note in the editor, as it does from a key.

#### `file.open_path` and `plugins.update`

- `file.open_path` opens a file by its absolute path, as File › Open any file does once it is
  chosen: its workspace's when one indexes it, else outside every workspace. A relative path fails.
- `plugins.update` updates a plugin by its name, as Manage plugins › Update does: its new commit is
  asked about first. A plugin's call may name only the plugin itself (`refused` otherwise), so one
  plugin never updates another. A plugin that finds itself older than the server it serves asks
  this way.

#### `view.redraw`: what the Rendered view draws from changed

`view.redraw` lays the GUI's Rendered view out again, the note's text unchanged. A block that draws
from a file beside the note is laid out once and kept until the note is edited, so a plugin that
writes such a file says so: autodoc-jupyter runs it after it keeps a cell's outputs, and the run's
outputs show under the cell. Without a Rendered view (the terminal) it does nothing. An AutoDoc
without it answers `unknown`, and the outputs show once the note is drawn again.

#### `buffer.new`: a draft for the user to edit

`buffer.new` opens a new buffer, in a tab of its own, and shows it: an unsaved draft holding the
plugin's text, the cursor at its start. A database plugin opens a query for the user to run this
way, a `SELECT … LIMIT 100;` on the table chosen. Its manifest lists it, `calls = ["buffer.new"]`,
and the plugin calls:

```go
ok, err := p.peer.Call("buffer.new", "public.users.sql", "SELECT *\nFROM public.users\nLIMIT 100;\n")
```

- **Its args are `[name, text]`**, both optional.
  - `name` is the draft's suggested name, `public.users.sql`. Its tab and the status line show it,
    the page reads as a file of that name would (a `.sql` draft has SQL's syntax and no Rendered
    view; with no name, the draft is Markdown), and the save picker starts with it.
  - It is a name, not a path: one with `/`, `\` or `..` fails, and nothing opens.
- **Nothing is written.** The draft is unsaved, like text typed into an empty page: closing it and
  quitting ask about it, an exit nobody chose keeps it for recovery, and only Ctrl+S, through the
  picker, writes a file. A name that is an open file's opens a draft of its own: that file's
  buffer and the disk are untouched.
- **The text is at most 256 KiB**; more fails (`failed buffer.new: the text is 300 KiB; at most
  256 KiB`), and nothing opens.
- **An AutoDoc without it answers `unknown buffer.new`**, so a plugin can tell its user to update
  AutoDoc.

## The SDK, in Go

```go
package main

import (
	"context"
	"os"

	"github.com/yongjohnlee80/autodoc/plugin"
)

type hello struct{ peer *plugin.Peer }

func (h *hello) Open(p *plugin.Peer, o plugin.Open) {
	h.peer = p
	f := plugin.NewFrame(o.Width, o.Height)
	f.Text(1, 1, "hello from a plugin", plugin.Style{FG: "cyan", Bold: true})
	_ = p.Frame(f)
}
func (h *hello) Key(k plugin.Key) {
	if k.Key == "q" {
		_ = h.peer.Close()
	}
}
func (h *hello) Resize(w, hh int)     {}
func (h *hello) Theme(t plugin.Theme) {}
func (h *hello) Close()               {}

func main() {
	if err := plugin.Serve(context.Background(), &hello{}); err != nil {
		os.Exit(1)
	}
}
```

- `Serve` answers the handshake, and calls the Handler's methods one at a time, in the order sent.
- It queues what it is sent itself, so a slow Handler never overflows the link.
- A plugin with a clock of its own (a game's gravity) sends from another goroutine. The `Peer` is safe
  for concurrent use.
- `Close` is called once, after `Open`, when the dialog closes or AutoDoc goes.
- A Handler that is also a `plugin.Hider` (`Hide()`, `Show()`) is told of hiding: a dialog's under
  `esc = "hide"`, a card's panel closing. A game pauses.
  A Handler that is not one keeps running while it is hidden.
- `plugin.open` comes after the dialog's first layout, with the size it was given; `plugin.resize`
  follows when the screen changes it.

**Protocol 2** adds optional interfaces, which `Serve` probes for as it does `Hider`. A notification
no interface takes is dropped.

| Interface | Method | Takes |
|---|---|---|
| `plugin.DocumentReader` | `Document(d plugin.Document)` | the feed. Only the newest waits for it: a newer document replaces one not yet handled |
| `plugin.Commander` | `Command(id string)` | a command |
| `plugin.Focuser` | `Focus(focused bool)` | a card's focus |
| `plugin.TableFetcher` | `FetchRows(f plugin.TableFetch)`, `TableClosed(id string)` | protocol 4: a fetch of a table's rows, and the table gone |
| `plugin.PointerHandler` | `Pointer(p plugin.Pointer)` | protocol 6: a left press or wheel step in the displayed frame |

`plugin.Base` is a Handler that does nothing. A service embeds it and adds what it needs:

```go
type preview struct {
	plugin.Base
	peer *plugin.Peer
}

func (p *preview) Open(peer *plugin.Peer, o plugin.Open) { p.peer = peer }
func (p *preview) Document(d plugin.Document)           { /* render d.Text, serve it */ }
func (p *preview) Command(id string) {
	if ok, err := p.peer.Call("file.save"); !ok { // protocol 3: one of the manifest's calls
		log.Print(err)
	}
	/* "open": open the browser */
}
```

**Protocol 3** adds `Peer.Call(id, args…) (ok bool, err error)`: it sends `host.call` and waits for
AutoDoc's answer.

- It may be called from any goroutine, the Handler's own methods included. The answer is taken off
  the link as it arrives, never queued behind the method waiting for it, so a `Command` or a `Key`
  that calls does not deadlock.
- Each call gets a number of its own; calls in flight together each get their own answer.
- `err` is a `*plugin.CallError` (`Code` refused, unknown or failed, and `Message`) when AutoDoc
  answered no; `plugin.ErrCallTimeout` when it did not answer within 10 s (a late answer is
  dropped); `plugin.ErrClosed` when the link is gone, for every call waiting then and after;
  `plugin.ErrNoCalls` when AutoDoc opened the plugin at a protocol before 3.

**Protocol 4** adds the table and the command's note.

- `Peer.Table(plugin.TableOpen{ID, Title, Fields, Rows, More})` shows a table, `Peer.Rows(id, rows,
  more)` answers a `FetchRows`, and `Peer.TableEnd(id, err)` ends the rows early. Each returns
  `plugin.ErrNoTables` when AutoDoc opened the plugin at a protocol before 4.
- A command that carries its note is handed to the `DocumentReader` first, in the same step, and
  then to `Command`: nothing sent later runs between the two, so `Command` reads the note it was
  chosen on. `plugin.Document` has `CursorByte` and `SelectionBytes` for slicing its `Text`.
- A document's `Path` is the note's path in its workspace, a slash path; `Root` is that workspace's
  folder on this machine, absolute. `d.File()` joins them: the note on disk, for a plugin that reads
  or writes beside it. A file outside every workspace has an absolute `Path` and no `Root`; a draft
  has neither, and `File()` is "".
- `plugin.FitCommand(id, d)` and `plugin.DocumentParamsAt(protocol, d)` are the forms AutoDoc sends,
  for a host or a test of your own.

```go
type sql struct {
	plugin.Base
	peer *plugin.Peer
	doc  plugin.Document
	rows *cursor // what serves a query's rows
}

func (s *sql) Open(p *plugin.Peer, o plugin.Open) { s.peer = p }
func (s *sql) Document(d plugin.Document)        { s.doc = d }
func (s *sql) Command(id string) {
	if s.doc.TooLarge { // the note was too large to send: say so on the card, run nothing
		return
	}
	stmt := statementAt(s.doc.Text, s.doc.CursorByte) // the note as it stood when "run" was chosen
	s.rows = query(stmt)
	page, more := s.rows.next(500)
	_ = s.peer.Table(plugin.TableOpen{ID: s.rows.id, Fields: s.rows.columns, Rows: page, More: more})
}
func (s *sql) FetchRows(f plugin.TableFetch) {
	page, more, err := s.rows.nextChecked(f.N)
	if err != nil {
		_ = s.peer.TableEnd(f.ID, err)
		return
	}
	_ = s.peer.Rows(f.ID, page, more)
}
func (s *sql) TableClosed(id string) { s.rows.close() }
```

**Protocol 7** adds the session slot, the close's reason and a command's arguments.

- `Peer.SetSession(values)` sends `host.session`. It returns `plugin.ErrNoSession` when AutoDoc
  opened the plugin at a protocol before 7, and `plugin.ErrSessionBounds` over a bound, sending
  nothing. A nil error means it was sent, not that AutoDoc kept it.
- `Open.Session` is the slot at this start (nil for none).
- `Peer.CloseReason()`, read inside `Close`, is `plugin.CloseQuit`, `CloseOff`, `CloseUpdate`,
  `CloseRemove`, `CloseFailure` or `CloseClosed`; `""` before 7.
- A Handler that is a `plugin.CommandArgser` (`CommandArgs(id string, args []string)`) takes every
  command in place of `Command`, with its arguments (nil for none).

```go
func (a *db) Open(p *plugin.Peer, o plugin.Open) {
	a.peer = p
	if tok := o.Session["token"]; tok != "" && a.valid(tok) { // signed in before this start
		a.token = tok
	}
}
func (a *db) signedIn(tok string) { _ = a.peer.SetSession(map[string]string{"token": tok}) }
func (a *db) Close() {
	if r := a.peer.CloseReason(); r == plugin.CloseQuit || r == plugin.CloseRemove {
		a.logout()
	}
}
```

**Protocol 8** adds `Peer.Notify(plugin.Notice{Message, ID, Ongoing})`, which sends `host.notify`.
It returns `plugin.ErrNoNotify` when AutoDoc opened the plugin at a protocol before 8, and an error
wrapping `plugin.ErrNotice` for a notice past a bound, sending nothing.

```go
func (a *db) run(sql string) {
	id := fmt.Sprintf("run-%d", a.next())
	_ = a.peer.Notify(plugin.Notice{Message: "running…", ID: id, Ongoing: true})
	res, err := a.query(sql)
	if err != nil {
		_ = a.peer.Notify(plugin.Notice{Message: "failed: " + err.Error(), ID: id})
		return
	}
	_ = a.peer.Notify(plugin.Notice{Message: fmt.Sprintf("%d rows", res.Rows), ID: id})
}
```

**Protocol 6** adds `plugin.PointerHandler`. AutoDoc delivers `plugin.pointer` only after the
protocol-6 handshake and only for unmodified input inside the frame. A right press still opens
the card's context menu. `Pointer.X` and `Pointer.Y` are zero-based frame coordinates; `Count` is
bounded to 1–16. A plugin can ignore pointer events and keep its keyboard controls.

## Drawing a card: the UI kits

A card or dialog can draw every cell itself with `plugin.Frame`. Most don't need to: the SDK has two
kits that draw it for you, in AutoDoc's look, keys and theme. Neither changes what AutoDoc and the
plugin say to each other; both send `host.frame` like any other plugin.

| | `plugin/card` (native) | `plugin/decl` (QML) |
|---|---|---|
| You write | a `Page` of blocks and controls, in Go | a golib QML layout, plus Go handlers |
| Controls | `Input` (and masked), `Buttons`, `List`, `Tree`, `Text`, `Line` | golib's widgets: `TextField`, `Button`, `ListView`, `TreeView`, `TableView`, `Flex`… |
| Starts | drawing at once | by parsing its layout |
| A minimal plugin, stripped | about 3 MB | about 7.5 MB |
| Example | `examples/card-go` | `examples/card-qml` |

Pick `plugin/card` for a form, a list or a tree beside the page; pick `plugin/decl` for a richer
screen, or one you'd rather write the way AutoDoc's own panels are written.

Both measure text as AutoDoc does: a CJK character takes two cells, and an accent written as a
combining mark, an emoji sequence or a flag stays one character. `plugin.Frame` does the same for a
plugin that draws its own.

### `plugin/card`

The plugin keeps its controls and builds a `card.Page` each time the card is drawn; the `Card` lays
it out, draws it and handles the keys:

```go
type people struct {
	plugin.Base
	peer *plugin.Peer
	card *card.Card
	name card.Input
	list card.List
}

func (p *people) page() card.Page {
	return card.Page{
		Title:  "People",
		Header: []card.Block{card.Line(card.Title, "People")},
		Body:   []card.Block{&p.name, &p.list},
		Keys:   "Tab: move · Enter: add · q: close",
	}
}

// the Handler's methods forward to the Card
func (p *people) Open(peer *plugin.Peer, o plugin.Open) { p.peer = peer; p.card.Open(peer, o) }
func (p *people) Resize(w, h int)                      { p.card.Resize(w, h) }
func (p *people) Theme(t plugin.Theme)                 { p.card.Theme(t) }
func (p *people) Focus(focused bool)                   { p.card.Focus(focused) }
func (p *people) Pointer(at plugin.Pointer)            { p.card.Pointer(at) }
func (p *people) Key(k plugin.Key) {
	if !p.card.Key(k) && k.Key == "q" {
		_ = p.peer.Close()
	}
}
```

- **The page** has a header (always shown), a body (scrolling to keep the focused row in view), a
  button bar, a keys line, a status and a problem. When the card is short, rows go first to the
  header, the focused row, the first problem row, the buttons and the keys.
- **Keys:** Tab and Shift+Tab move between controls. Lists and trees take j k, PageUp PageDown (Ctrl+U
  Ctrl+D), g G and Enter; trees open with l or Right and close with h or Left. `Card.Key` returns false
  for a key it did not use, so your own letters (and q) stay yours. In an `Input`, every printable
  key is text, q included.
- **The pointer** (protocol 6): a press focuses and selects what it lands on and presses a button;
  the wheel moves the focused list or tree.
- **Theme:** the selection wears the theme's highlight, and the inactive one while the card lacks the
  keys. Text roles are `Plain`, `Dim`, `Title`, `Good` and `Problem`.
- A `List` asks for only the rows in view, so it can hold thousands. A `Tree` keeps its selection by
  node: when the node closes away or is removed, the selection goes to its nearest ancestor still
  showing.
- A masked `Input` never makes its value a string to draw it; read it with `Runes()` and `Clear()` it
  when done, which zeroes it.
- `card.Options{Modal: true}` for a modal dialog, which AutoDoc sends no `plugin.focus`.

### `plugin/decl`

`decl.New` is a whole `plugin.Handler`: pass it to `plugin.Serve`, or embed `*decl.Card` in a type
that adds `Command`, `Document` or tables.

```go
c := decl.New(decl.Options{
	Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", layout),
		tuidecl.Singleton("people", "1.0", "App"),
		tuidecl.Sources(map[string]any{"App.status": ""}),
		tuidecl.Handlers(map[string]gdecl.HandlerFunc{"App.add": add}),
	},
	Setup: func(p *tuidecl.Program, _ *plugin.Peer) error { return p.Set("App.status", "ready") },
})
_ = plugin.Serve(ctx, c)
```

```qml
import tui 1.0
import autodoc.plugin 1.0   // Theme, kept to AutoDoc's theme
import people 1.0

Window {
    palette.window: Theme.app.window; palette.windowText: Theme.app.windowText
    Text { text: App.status }
}
```

- **The theme:** bind the layout's palette to `Theme.*` and name no colour; when AutoDoc's theme
  changes, the palette follows without a rebuild, and what was typed or scrolled stays.
- **State** changes on the program's loop only: in `Setup`, before the first frame; in a handler; or
  through `Card.Post` from your own goroutines. A `Post` after the card closed does not run.
- **Keys** reach the program as a terminal's would, except Esc, which AutoDoc keeps to close or hide
  the card. A left press arrives as a press and its release.
- A layout that fails to build, or a `Setup` that fails, is shown in the card in red, and the card
  closes. A program that quits closes its card.

### Testing a card

Both kits run without AutoDoc. `card.Card.Attach` draws to any `Frame`/`Title` host, so a test can
send keys and read frames. The examples go further, driving the plugin over the protocol as AutoDoc
does (`examples/internal/hosttest`).

To try an example from a checkout, run `go build -o bin/autodoc-card-go .` in its directory.
Installing from a git URL is for a repository whose top holds `plugin.toml`, as
`autodoc-doc-stats` does; to install one of these, copy its directory into a repository of its own.

## What a plugin may not do

- **Change the note** but through a command it declares in `calls` (a Lua command of yours, say). A
  plugin reads the feed; there is no protocol to write a document directly.
- **Run what its manifest does not declare,** or any plugin's command.
- **Highlight the editor's text.** The highlighter runs on every frame, synchronously, which a
  process round-trip cannot meet. New languages are AutoDoc's own work.
- **Draw outside its surface, or add to AutoDoc's menus** other than its Plugins entry and its
  declared commands.
- **Run inside AutoDoc.** A plugin is a process: there are no in-process (JavaScript, Lua, WASM)
  plugins, and no hub. A plugin's source is its git URL.
