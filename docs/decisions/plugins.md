# Plugin decisions

Why AutoDoc's plugins work the way they do: what a plugin is, how it contributes to the editor, how
users find and install one, and the kits a plugin draws with. Each section states one decision, its
reason, and where it lives in the code. The reference a plugin author writes against is
[plugins.md](../plugins.md); this page does not repeat it.

## Plugins are separate programs

A plugin is a directory in the plugins folder beside the config (`$XDG_CONFIG_HOME/autodoc/plugins/`)
with a `plugin.toml`. The TUI starts its command as a child process and the two exchange msgpack-RPC
notifications over stdin and stdout, never requests. A plugin can be written in any language, a
crash or a hang costs only its own window, and the close is bounded (`plugin.close`, then SIGTERM,
then SIGKILL).

- There is no embedded scripting engine for plugins and no sandbox. A plugin runs as the user, as a
  Neovim plugin does, and its process can reach the daemon's whole API. Lua scripts are the
  in-process extension; plugins are the out-of-process one.
- There is no hub to install from: a plugin's git repository is its source. Every add and update
  says first that it is at the user's own risk, and the Plugins menu adds, updates and removes them.
- The dialog was the first extension point. Esc always belongs to the host, so no plugin can trap
  the user.

In the code: `tui/plugins.go`, `tui/pluginrun.go`, `tui/plugininstall.go`, `plugin/protocol.go`

## The Go SDK

The protocol and the SDK a plugin is written with live in AutoDoc's own `plugin` package, which
imports golib and nothing else of AutoDoc's. `plugin.Serve` takes a `Handler` and runs the transport,
so a plugin writes handlers, not framing. Each capability a later protocol added is an optional
interface (`DocumentReader`, `Commander`, `Focuser`, `CommandArgser`, `SurfaceHandler`, …) that
`Serve` probes for; a notification nothing handles is dropped. `plugin.Base` gives no-op methods, so
a plugin built against an older SDK compiles and runs unchanged.

In the code: `plugin/serve.go`, `plugin/protocol.go`, `plugin/link.go`

## Protocol versions and strict manifests

A host speaks a range of plugin protocols (today 1 to 11) and speaks to each plugin the one its
manifest names: `plugin.open` carries it and `host.ready` must echo it. The protocol rises whenever a
manifest can declare something an older host must not run without, when a meaning changes, or when
a plugin must handle something new. So every protocol's additions are declared in the manifest, a
plugin is never sent what it did not ask for, and an older AutoDoc lists a newer plugin disabled
rather than running it half-working.

The manifest is decoded strictly. An unknown key at any level disables the plugin with the key
named, because a key the host ignored would be a feature it silently ran without. A manifest that
uses a key of a later protocol than it says is disabled the same way.

In the code: `core/manifests/manifests.go`, `plugin/protocol.go`

## Declared contributions

What a plugin does in AutoDoc is read from its manifest, not described separately. One function
rolls the manifest's declarations up (a card, the feed, its commands, the AutoDoc commands it may
call, a table, the files it opens), and the same list appears in the install confirmation after the
risk warning and in the registry's listing. `core/manifests` has no UI dependencies, so the
registry's tools validate manifests with the same code AutoDoc installs with.

In the code: `core/manifests/manifests.go`, `tui/plugininstall.go`

## Cards

A card (`[dialog] modal = false`) is a docked panel of its own beside the page, as AutoDoc's other
panels are. It takes the keys only while focused (a click, or one of its commands, focuses it) and
the plugin hears `plugin.focus` both ways. A modal dialog stays the default, because a game needs
every key.

Closing a card's panel hides it and leaves its plugin running: Esc, its menu's Close, its own
`host.close`, and its `toggle` command while the panel is open all hide it, and the plugin is told
`plugin.hide`. A signed-in plugin therefore stays signed in across a hide. Only Turn off (or an
update, a removal, a fault, or quitting) stops the process.

In the code: `tui/plugincmds.go`, `tui/pluginpanels.go`, `tui/pluginview.go`

## The document feed

A plugin that declares `[feed] document = true` is sent the open note as it stands, saved or not:
its text, cursor and selection, after each 300 ms pause in editing or moving. Lines and columns
count grapheme clusters, as the editor's cursor does, and `version` rises with every edit so a
plugin can drop stale answers.

- **The feed is state, not events.** On both sides only the newest document waits: a newer one
  replaces one not yet sent, so a slow plugin never builds a backlog. Every other notification keeps
  its order and is never dropped.
- **The bound is the whole encoded notification**, at the link's own 1 MiB limit. A larger note is
  sent as `too_large` with its path and version and no text, so the plugin can say so instead of
  working on an old copy.
- From protocol 4 a command carries the document as it stood when the command was chosen, because a
  feed document may arrive after the command, or not at all.

In the code: `tui/pluginfeed.go`, `tui/pluginqueue.go`, `plugin/link.go`

## Services

A service (`kind = "service"`) has no surface: its `plugin.open` has no size, and its frames and
titles are dropped. It starts on its first command or feed event (`start = "use"`, the default), or
with the TUI (`start = "launch"`), and runs until AutoDoc quits or it sends `host.close`. A failed
service is not restarted until its next use.

In the code: `tui/pluginrun.go`, `core/manifests/manifests.go`

## Plugin commands and the SPC p chooser

A manifest's `[[commands]]` are on the Plugins menu under the plugin's entry, and on the plugin's
card behind SPC p. SPC p first opens a chooser of the plugins that have commands; a plugin's letter,
or moving to it and Enter, shows its card, where a command's letter runs it, and Backspace goes back.

- **Letters are scoped.** A command's letter need only be unique within its own plugin, so two
  plugins can both use `s`, and installing a plugin never moves another's letter. A single shared
  card made every install a source of collisions.
- **Plugin letters are the user's.** `*` on the chooser assigns one; giving a letter to one plugin
  takes it from any other. They are global preferences, because plugins are installed per user.
- **The chooser shows even with one plugin installed,** so SPC p's keys do not change meaning when
  a second one arrives.
- The chooser is text with nothing focusable in it, as the leader card is, so every key reaches its
  shortcuts and a letter like `g` is never swallowed by a list.
- Plugins never take AutoDoc's own leader letters.

In the code: `tui/pluginpick.go`, `tui/plugincmds.go`, `tui/qml/views/PluginChooser.qml`

## The published reference

[plugins.md](../plugins.md) is the plugin protocol's reference and is kept in the repository beside
the code. Tests hold the two in step: every notification constant must be documented there, and its
manifest samples must decode to runnable plugins. `examples/python/echo_plugin.py` speaks the
protocol with no AutoDoc code at all, so the protocol is shown to be language-neutral.

In the code: `docs/plugins.md`, `examples/python/echo_plugin.py`, `tui/plugins_test.go`

## One command catalog

Every command a user can run is one catalog entry, wherever it was declared: AutoDoc's built-ins, a
feature's `Catalog()`, a Lua `autodoc.command`, and a plugin's `[[commands]]`. Before, each of these
was reachable only where it was declared. Now `:`, F1, keys, SPC p, Lua and plugin calls all read
the same list.

- An id is `<subject>.<action>` (`file.open`, `terminal.toggle`). `lua.<name>` is Lua's alone and
  `plugin.<plugin>.<id>` that plugin's alone; a feature may use neither.
- A duplicate id is refused when it registers, and a registration is checked whole (id, verb and
  every alias) before any part is taken.
- The catalog holds only what a person would invoke. The QML handlers stay the document's wiring;
  an entry's `Run` calls the same function as its handler twin, so there is one behaviour.

In the code: `tui/catalog.go`, `tui/builtins.go`, `tui/plugincmds.go`

## One owner per command name

`:` is built from the catalog: an entry with a verb is a `:` command, and keeps its aliases, its raw
argument line and its bang. Each `:` name has exactly one owner at a time. A Lua command may take
any name, golib's own included, and only that name moves to it: overriding `term` leaves
`:terminal` working, and the name goes back when the Lua command goes. A built-in or feature entry
that takes golib's verbs or another entry's name stops the program at start. F1, completion and the
editor's command registry read the same name map, so they always agree on who owns a name.

In the code: `tui/catalog.go`, `tui/scripts.go`

## Keys and help read the catalog

A key may run a catalog entry by id (`Key.Cmd`) instead of carrying its own QML, and the key check
refuses an id that names no entry. Only feature entries may be bound this way: they are fixed when
the registry is built, while Lua and plugin entries come and go at runtime. F1's COMMANDS section is
generated from the catalog, grouped by source, with each entry's verb and keys, and the SPC p cards
are made from the plugin entries.

In the code: `tui/catalog.go`, `tui/keys_test.go`, `tui/help_test.go`

## Lua runs catalog entries

`autodoc.run(id, args…)` runs any catalog entry on the UI loop and answers `ok, err`; the catalog is
the whitelist, so there is no second list of what a script may run. A Lua entry run this way, by a
key or by a plugin's call still gets the editor it edits. `autodoc.run` refuses `plugin.*` ids, and
so does a plugin's `host.call`: one extension may not drive another, which keeps the call graph
shallow.

In the code: `tui/scripts.go`, `tui/luahost.go`

## Plugin calls

From protocol 3 a plugin may ask AutoDoc to run a catalog command: `host.call {call, id, args}`,
answered by `plugin.result {call, ok, error}`. Both are notifications matched by the call number,
which gives request and answer without a request frame on the link.

- **Only declared commands run.** The manifest's `calls` lists exact ids (no wildcards), shown
  before install. Plugins are not sandboxed, so what one may do through AutoDoc's own commands has
  to be visible and reviewable in its manifest.
- **Every accepted call is answered exactly once:** ok, failed, unknown, or refused. A call that
  reuses a number still pending is a protocol violation; it is logged and never answered, so the
  original's one result is the only one with that number. At most 64 calls wait per plugin.
- **In the SDK,** `Peer.Call` has a 10 s bound and is safe from any goroutine, including a handler.
  Results bypass the handler queue, so a handler that calls never waits on a result queued behind
  itself.

In the code: `tui/plugincall.go`, `plugin/serve.go`

## Plugin sessions

From protocol 7 a plugin keeps what must outlive its process, such as a sign-in token, in a session
slot the host holds: `host.session {values}` replaces it, and every later `plugin.open` in this run
of AutoDoc hands it back. A plugin turned off and on, updated, or restarted after a crash checks its
token instead of asking again.

- The slot lives in memory only, until AutoDoc quits or the plugin is removed. Nothing writes or
  logs its values, and it is bounded (32 keys, 8 KiB a value, 64 KiB in all).
- The slot has one writer on the UI loop, which accepts a write only from the plugin's live run that
  is not stopping. An old process cannot overwrite its replacement's slot or bring back a removed
  plugin's.
- `plugin.close {reason}` says why: quit, off, update, remove, failed or closed. The first stop's
  reason is the one sent. Delivery is best effort: a plugin that does not answer is stopped by the
  signals.
- `plugin.command` may carry arguments.

In the code: `plugin/session.go`, `tui/pluginfeature.go`, `tui/pluginrun.go`

## A plugin's Lua package

A plugin can ship a Lua package in its `lua/` folder, which is how it adds `:` commands. AutoDoc
loads it after the user's own packages, gated as `plugin:<name>`, its requires asked with the
prompt every package uses, its `require` confined to its folder. It comes and goes with the plugin.

- A user's package of the same name wins by reservation: even a broken one keeps the name, and the
  plugin's package never runs, not even its init.
- `autodoc.plugin.run(id, args…)` runs a command of the package's own plugin, starting it when
  needed. The caller is identified by the gate, and the host dispatches to its own record of the
  package's plugin, never to a name the script passes. Any other script is refused.

In the code: `tui/luapackages.go`, `tui/luahost.go`

## Notifications

From protocol 8 a plugin posts notifications with `host.notify`, and Lua with
`autodoc.notify(message [, opts])`: a plain toast, or one that stays and is updated in place by its
id while ongoing, and ends. Every end is kept in the history. A notification is a protocol message
and not a catalog command, because the host must know which run sent it to do three things:

- namespace its ids, so no plugin touches another's toasts or AutoDoc's own;
- refuse a notice from a run that is stopping, behind the same fence as the session slot;
- end, when a plugin stops, every id it left ongoing.

Only active ids are tracked (at most 16 per run), and messages and ids are bounded. A refusal names
the rule in the plugin's log, never the message.

In the code: `tui/notify.go`, `plugin/notify.go`, `tui/pluginrun.go`

## Editor menu rows

Lua's `autodoc.menu` rows and a plugin's `[[menu]]` rows (protocol 11) go into one registry, keyed
by owner and id, so scoping, submenus and removal behave the same for both. The editor's right-click
menu asks the registry for its rows each time it opens. A plugin's rows sit under a submenu of its
title, each runs one of its own commands, and they go when the plugin is removed or updated.

A plugin changes a row's title or disables it by pushing `host.menu`; the host keeps the latest and
shows it at the next open. The host never asks the plugin as the menu opens, so a busy plugin never
slows a right-click. A stopped plugin's pushed titles are cleared, and its rows show as the manifest
declares them.

In the code: `tui/menurows.go`, `plugin/menu.go`, `core/manifests/manifests.go`

## Card panels made at runtime

The card set follows the plugins folder. A card plugin installed, updated or found while AutoDoc
runs gets its panel at once, and a removed one's panel goes with it, its plugin stopped, with no
restart.

- The panel registry snapshots every feature's panels once, at build. The plugins feature is the one
  exception: its cards are staged, checked exactly as at build (unique ids, geometry bounds), and
  committed only after the document reloaded with them. A feature that holds its panel host cannot
  be restaged, since it would keep the old host.
- A refused stage or reload puts the card set back, and the registry and the document are as they
  were. A reload that failed part-way leaves a screen only a restart can rebuild, and AutoDoc says
  so.
- An added card keeps its stored geometry (`tui.card_<name>.*`).

In the code: `tui/pluginpanels.go`, `tui/panelregistry.go`

## Shared panels and their geometry

The docked panel component and the codec for panel geometry preferences live in golib and are
shared with the AutoDB app, so neither app keeps a private copy. AutoDoc's QML imports golib's
`Panel`, and every geometry write passes golib's one check: an exact panel id, an edge that panel
may dock to, and a value within its bounds. Anything else is refused and never stored.

In the code: `tui/panelsize.go`, `tui/panels.go`, `tui/modules.go`, `tui/qml/main.qml`

## The registry

The [plugin registry](https://github.com/yongjohnlee80/autodoc-plugin-registry) is one repository
with one entry per plugin, Lua package and KB type. Its CI validates each entry and publishes an
`index.json` that AutoDoc reads. A git URL still installs anything; the registry is for discovery.

- **An entry holds no capability list.** What a plugin declares comes from its own manifest, read
  with `core/manifests` at the audited commit, so a second list can never drift from the first.
- **AutoDoc fetches the index only when a list opens,** never at startup or in the background, at
  most once a day and conditionally on the cached ETag. A response replaces the cache only once it
  validates; otherwise AutoDoc uses the cache, then the built-in KB types, and says which.
- Plugins › Browse the registry…, `:lua-install <name>` and Add from a git URL take a name from the
  index as well as a URL.

In the code: `core/registry/registry.go`, `tui/registry.go`, `tui/registrybrowse.go`, `tui/kbtype.go`

## Audits are per commit and advisory

An entry carries its audits, each naming a full commit (a tag can move; a commit cannot), the model
and the date. The install card says whether the exact commit it would install was audited, when an
older one was, or that none was. The risk warning stays either way: an audit is a reading of one
commit, not a guarantee, and AutoDoc never parses or scores an agent's verdict.

**Audit…** on the card hands a copy of the candidate to one of the user's own agents with a task
file, and says first that AutoDoc cannot stop that agent from running code or changing files. The
registry's periodic audit and the in-app one read the same prompt, `docs/audit-prompt.md`, so both
ask the same questions.

In the code: `agents.go`, `tui/candidate.go`, `docs/audit-prompt.md`

## One candidate per install or update

What is audited is what is installed. Every add, update and Lua package install stages one
candidate: a fresh clone pinned to a full commit. Its card names that commit, each Audit… gets a
fresh copy of it, and Install builds it. The installed tree is never audited or built from.

- Install checks first that the stage is still pristine at its commit.
- Two live candidates never share a target, and a candidate ends exactly once, removing its stage
  and audit copies.
- An update stops the running plugin only after the new build succeeds, then swaps the folders by
  rename, keeping the old one in `plugins/.previous/<name>` until the next update. That is why a
  plugin must keep its state outside its install folder.

In the code: `tui/candidate.go`, `tui/plugininstall.go`, `tui/luapackages.go`

## The native card kit

`plugin/card` is the UI kit for card and dialog plugins written in Go, so each plugin no longer
writes its own layout, wrapping, focus, lists and colours, and drifts from AutoDoc's. The plugin
keeps its controls (`Input`, `Buttons`, `List`, `Tree`) as values, and on every draw returns a
`Page`; the `Card` fits it to the size, keeps the focused row in view, draws it in the theme's roles,
and handles Tab focus and the pointer. Every button is its own Tab stop. The AutoDB plugin's card
was ported onto it as the proof.

The wire did not change. `plugin.Frame` places grapheme clusters by their display width, and the
host draws a frame a cluster at a time, so accents, emoji and wide characters line up in both kits.

In the code: `plugin/card/page.go`, `plugin/card/card.go`, `plugin/frame.go`, `tui/pluginview.go`

## QML cards run in the plugin

`plugin/decl` runs a golib QML program as a card or dialog inside the plugin's own process, on a
backend with no terminal: its screen is a grid, and every flushed frame is sent whole as
`host.frame`. The host never parses or runs a plugin's QML, which keeps it stable and keeps the
protocol as it is. Host notifications become the program's events (keys, resizes, the pointer, focus)
and the theme becomes `Theme.*` sources, so a layout follows AutoDoc's theme without being rebuilt.
Esc never reaches the program.

In the code: `plugin/decl/decl.go`, `plugin/decl/backend.go`, `plugin/decl/events.go`

## Pixel surfaces

From protocol 10 a card or dialog may draw pixels instead of cells: a canvas, a picture, a web page.
Its `[surface]` asks for one. In the GUI the surface fills the card's area; the protocol on stdout
stays the control channel, and frames travel beside it. The pointer arrives in dp. The host keeps IME
composition, drawing the preedit at the caret the plugin reports and sending committed text as keys.
A malformed side-channel message closes the plugin. Linux ships first, with host-allocated shared
memory; elsewhere a surface plugin draws cells.

In the code: `tui/pluginsurface.go`, `plugin/surface_protocol.go`, `plugin/surface_linux.go`, `examples/surface-go/main.go`

## The surface side channel

A frame cannot cross the stdout protocol: it is capped at 1 MiB and cannot pass a file descriptor.
So the host makes a socket pair at start and hands the plugin its end as fd 3, named in
`AUTODOC_PLUGIN_SURFACE_FD`. One datagram is one msgpack map, at most 2 KiB with at most 4 fds as
`SCM_RIGHTS`.

- A received descriptor belongs to a guard until the message is admitted, and every other path
  closes it, so no malformed message leaks one. On Linux close-on-exec is set atomically at receive.
- The plugin must set close-on-exec on fd 3 before it starts any process (the Go SDK does). That is
  hygiene against accidental inheritance, not a security boundary, and the host never relies on it.

In the code: `plugin/surfaceconn_linux.go`, `plugin/surfacewire.go`, `tui/pluginrun.go`

## Surface buffers and generations

- **One owner.** A single goroutine owns the surface's buffer registry, and everything else (the
  reader, release callbacks, the UI loop) posts to it. The host allocates the buffers as sealed
  memfds it maps read-only, and answers each frame's outcome (`frame.done`) before its buffer's reuse
  credit (`buffer.free`), each exactly once.
- **Generations.** A generation is one size, scale and set of frame kinds. A change starts a new one,
  paced by the plugin's acknowledgement, and retires earlier buffers on both sides. The plugin
  matches `plugin.surface` and its buffers by generation, never by arrival order, since they come on
  separate channels.

In the code: `tui/pluginsurface_linux.go`, `plugin/surface_linux.go`

## Surfaces without pixels

The terminal draws no pixels, so `plugin.surface` is never sent there and the plugin draws
`host.frame` cells as any plugin does. A plugin whose surface is `required` is not opened where
there are no pixels. In the GUI the card shows its own background and title until the first frame is
drawn.

In the code: `tui/pluginsurface_tui.go`, `tui/pluginsurface_other.go`, `tui/pluginview.go`

## Lab notes

A lab note is an ordinary Markdown note named `*.lab.md`, so it stays indexed, linked and rendered
like any other. Its fenced code cells run in a Jupyter kernel through the Jupyter plugin, which lives
outside AutoDoc's module. A run's outputs are files in the note's companion folder
(`plan.lab/out/<cell key>/`), never spliced into the note, so a run does not rewrite the note or
race the user's typing. The GUI's Rendered view and the HTML export draw each cell's outputs under
its fence.

The cell key is a hash of the fence's language and its code, computed in one package that AutoDoc
and the plugin both read, so a cell's outputs cannot silently stop showing. Editing a cell changes
its key, and its old outputs stop showing until it runs again. A plugin's document carries the
workspace's absolute root, so it can write beside the note.

In the code: `core/lab/lab.go`, `tui/labcells_gui.go`, `core/export/export.go`, `plugin/protocol.go`

## Plugins that open file types

From protocol 9 a plugin's `[open]` claims file extensions. However such a file is opened, AutoDoc
hands its absolute path to the plugin's named command instead of opening it, so an `.ipynb` opens as
a lab note rather than raw JSON. The open goes through the existing command path (start on use,
show the card, pass arguments) rather than a new message. A plugin never takes AutoDoc's own
extensions, so a note it writes and opens never comes back to it, and when two plugins claim one
extension the first in the list wins and the other is shown as shadowed.

In the code: `tui/files.go`, `tui/pluginmanifest.go`, `core/manifests/manifests.go`

## Plugin tables in the results panel

A plugin with rows to show does not draw them. From protocol 4 it sends `host.table` with the columns
and a first page, and AutoDoc shows them in its own results panel, asking for more pages as the user
reads down and telling the plugin when the table closes. One table shows at a time, and at most
100 000 rows load, so AutoDoc never holds a whole large result. Sorting, filtering, copies and
exports act on the loaded rows and never fetch. From protocol 5 a plugin may send each cell's kind,
so NULL, numbers, booleans and JSON keep their meaning in sorting, filtering and exports. The AutoDB
plugin is the first consumer.

In the code: `tui/plugintable.go`, `tui/plugintable_export.go`, `plugin/protocol.go`

## Opening files by path and self-update

Two catalog commands serve plugins that manage their own files and versions. `file.open_path` opens a
file by its absolute path, for a plugin's call that has no dialog to answer. Such a file may be
outside every workspace, and it follows the disk as a workspace file does: clean, it reloads; with
unsaved edits, it is badged and keeps them. `plugins.update` updates a plugin by name, asked first.
Through `host.call` a plugin may name only itself, so one plugin never updates another.

In the code: `tui/builtins.go`, `tui/pluginfeature.go`, `tui/plugincall.go`
