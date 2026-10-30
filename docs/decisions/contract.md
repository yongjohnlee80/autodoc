# Contract decisions

How AutoDoc is split into a daemon and its clients, what the API between them promises, and what
a Lua script or a KB type may do through it. Each section says what was decided, why, and where it
lives in the code. [architecture.md](../architecture.md) describes the packages and the module
convention in full; this page records the choices behind them.

## One binary, modes as flags

AutoDoc is one binary, `autodoc`, and each of its modes is a flag: `--serve` is the daemon, `--ui`
the terminal UI, `--gui` the window, `--call` one verb from a shell, `--contracts` the verb
catalog, and the `--check-*` flags validate a file without a UI or a daemon. `app.Main` parses the
flags and runs the mode, and a `main` package only hands it its version.

One binary means one install and one version to match: the UI and the daemon it starts are always
the same build. The window needs cgo, so it is behind the `gui` build tag, and a build without it
answers `--gui` with an error that says how to get one.

In the code: `app/app.go`, `app/gui_off.go`, `cmd/autodoc`

## The daemon's start order

`--serve` starts its parts in a fixed order, and stops them in reverse: bind the socket, open the
store, start every workspace (its indexer and its follower), open the files, then serve RPC. Each
step registers how to undo itself, so a failure part way stops only what started.

- **One daemon per store.** If the socket answers with an AutoDoc serving the same store, the new
  process exits as "already serving". The store also holds a file lock (the lease), so a second
  daemon on another socket cannot serve it either.
- **Clients start it.** `--ui` dials the socket and starts `--serve` when nothing answers, so the
  user never starts the daemon by hand.
- **Local only, no login.** The daemon listens on a unix socket that only its user can reach. It
  serves that user's own files, which the user can already read, so a login would add nothing.

In the code: `app/serve.go`, `app/socket.go`, `core/store/lease.go`, `internal/client`

## The index follows the files

The files are the truth and the index is derived from them. Anything may change a file (an
editor, git, an agent), so the daemon follows each workspace's root rather than expecting to be
told.

- The follower watches the root (a native watch when the filesystem has one, polling otherwise),
  and only then scans it against the index, so a change made during the scan is already in the
  watch stream.
- An event only names a path. The indexer, the store's one writer, reads the file's current state
  and decides: delete a document that is gone or no longer eligible, skip one whose version is
  unchanged, re-index the rest.
- When neither a watch nor polling can be set up, the follower is `degraded`: it rescans the whole
  root every poll interval and retries the setup with backoff. A subtree it cannot read keeps its
  indexed documents and is rescanned until it reads. `index.status` reports which of these is
  happening.

In the code: `core/follow`, `internal/daemon/workspaces.go`

## AutoDoc's own writes take the same path

AutoDoc's editor writes a document with `doc.write`, conditional on the version it read: an empty
version creates the file and never replaces one, and a stale version is a `Conflict`. The write goes
to the filesystem, and the follower picks it up as it would an outside edit, so the engine has one
path from a file to the index.

A write that landed but whose follow-up step failed answers `Committed`, with no version. The
client reads the file and compares; it never sends the write again.

In the code: `core/docs/docs.go`, `rpc/doc.go`

## One RPC API for every client

Every client (the TUI, the GUI, `--call`, the Neovim plugin, agents) speaks one msgpack-RPC API,
and none has a private path. The `rpc` package is a projection of the core packages, with no logic
of its own.

- **Handshake first.** A session says `sys.hello` with a protocol the build serves before any other
  verb answers; a mismatch refuses the session for good. The TUI must match the daemon's protocol
  exactly. A verb arrives at a protocol, and a session below it sees an unknown method.
- **The server only answers.** It never sends a notification or a request, so a
  request/response-only client can use every verb. Clients follow changes by pulling cursors
  (`index.changes`, `sys.events`, `todos.changes`), and an expired cursor tells the client to re-list
  and resume.
- **Error codes say what to do next.** Each code is chosen by the client's next step (say hello,
  re-list the workspaces, read and merge, search lexically), and only a fixed message crosses the
  wire, never an internal error's text.

In the code: `rpc/server.go`, `rpc/errors.go`, `rpc/hello.go`

## Features as modules with verb contracts

A daemon feature is a core package plus a `Module` that declares its verbs, each as a contract
(name, protocol, scope, params, result, errors, event, effect), and its public errors. Everything
else is derived from the contracts: registration and the protocol gate, the event log, error
mapping, `sys.contracts`, `autodoc --contracts`, AGENTS.md's tables and the Lua namespaces. A UI
feature is a `Feature` that sees a narrow `Core`, never the host.

This gives AutoDoc one place per verb instead of a handful of hand-kept copies that drift apart. The
convention itself is described in [architecture.md §4](../architecture.md#4-the-convention).

In the code: `core/module`, `rpc/contracts.go`, `rpc/module.go`, `app/serve.go`, `internal/contractsgen`

## Core packages reach files through vfs

A core package never calls `os` for a file. Every filesystem operation goes through golib's `vfs`,
and a `vfs/local` filesystem is built outside `core/`, where the daemon opens a workspace's root.
This keeps the core testable on an in-memory filesystem, and confines every operation to the root
it was opened on. See [architecture.md §3](../architecture.md#3-dependency-rules).

In the code: `core/docs`, `core/workspace`, `internal/daemon`

## Scripts call the daemon through its contracts

A Lua script reaches every daemon verb as `autodoc.call(verb, params, cb)`, and as generated
functions such as `autodoc.search.query(…, cb)`. The TUI builds the namespaces from the daemon's
`sys.contracts` on each connect, so they are the verbs that daemon offers. Calls are asynchronous:
the verb runs off the UI loop, and `cb(result, err)` runs back on it. A verb of the protocol's own
(`sys.hello`, `sys.shutdown`) is refused before it is sent.

In the code: `tui/scripting.go`, `script`

## Every verb states its effect

Every contract names its effect: `read`, `write` (creates, changes or removes documents and tasks)
or `settings` (the daemon's own configuration). The effect is in `sys.contracts` and
`--contracts`, and it decides which capability a gated script needs to call the verb.

A contract that names none fails a test, so a forgotten classification cannot ship. A verb whose
effect the TUI does not know (an older daemon that does not send it) is refused to every gated
script, whatever it was granted: no grant can be known to cover it.

In the code: `core/module/module.go`, `rpc/contracts.go`, `internal/contractsgen`

## Gated scripts and their capabilities

AutoDoc's embedded scripts and the user's own `lua/*.lua` run ungated. An installed package runs
gated: it declares what it needs as it loads, `autodoc.requires{"workspace.read"}`, and the user is
asked once per script.

- **The grants are the script's own.** A gated script runs in an environment of its own, whose
  `autodoc` is a proxy over the real table. A function looked up through it checks the script's
  grants at the moment it is called. A function the script defines keeps that environment, so its
  grants follow it into callbacks, handlers and keys, with nothing to track at run time.
- **Refuse the call, not the script.** A call without its grant returns `nil, "needs <capability>:
  not granted"`. The script loads and runs what needs nothing, and a grant given later works at once.
- **The capabilities:** `workspace.read`, `workspace.write` and `settings` by the verb's effect;
  `clipboard`; `agents`; and `commands` for anything that runs a command line (`autodoc.run`,
  `ed:run`, and the keys and menu rows a gated script binds).

In the code: `script/gate.go`, `tui/scripting.go`, `tui/luapackages.go`

## Grants are remembered as a yes

The user's answers are kept in the `lua.grants` preference, in the daemon's store. Only an Allow is
kept. A No looks the same as closing the card, pressing Esc or quitting, so remembering it would turn
a dismissal into a permanent refusal; a refused capability is asked about again at the next start.

In the code: `tui/luagate.go`, `tui/prefs.go`

## A gated script's files stay in the workspace

A gated script may use `file.*` and every other path verb, but only inside the workspace: reads
inside a served workspace's root, writes inside the current one. The TUI's gate decides that a call
is a gated script's and attaches its bounds, `{within, protect}`, as one parameter past the verb's
own (from protocol 34), replacing anything the script passed. The daemon enforces them.

- **Enforced where the files are reached.** Every served workspace's filesystem is wrapped in a
  guard that checks each name it is given against the request's bounds. A verb added later is
  bounded with nothing to declare. Bounded `file.*` calls are routed into the workspace that holds
  the path, and the unbounded file helpers refuse any call that carries bounds.
- **Checked whole.** The complete path is checked before the operation, its existing part with links
  resolved and the part not yet made appended: it must stay in the root, have no hidden component
  (except AutoDoc's own `.autodoc/`, `.todo-list/` and `.autodoc-vars.toml`), and not be one of
  AutoDoc's own folders (the store, the state folder, the config), matched by identity. A file with
  a second hard link is refused, and a folder is never moved, copied or removed.
- **No new roots.** A verb that adds a root or moves where files are written is refused to a bounded
  call, since it would move the bounds themselves.
- The operation then runs through the workspace's own `os.Root`, so it cannot leave the root even
  if something changes after the check. A call without bounds is the user's, unchecked as before.

In the code: `core/fsbounds`, `rpc/bounds.go`, `rpc/files.go`, `rpc/module.go`, `internal/daemon/workspaces.go`, `internal/localfs/localfs.go`

## User-only commands

Granting `commands` lets a script run AutoDoc's commands, but never as the user. A catalog entry
marked `UserOnly` runs code, launches a path, fetches and writes without asking, or changes what
scripts may do, and a gated script is refused it however it gets there (`autodoc.run`, `ed:run`, a
key or menu row it bound). The Lua shell, opening a file with the default app and applying a KB
type are user-only.

For the same reason a gated script may not write the two preferences whose value is rights or a
command: `lua.grants` and the agent profiles. Reading them is an ordinary read.

In the code: `tui/catalog.go`, `tui/builtins.go`, `tui/scripting.go`

## Path commands run within the workspace

A catalog entry that opens or saves a path is marked `Path`. Run by a gated script, it takes the
same bounds as the script's `file.*` calls, checked by the daemon: a file opens only if a bounded
read reaches it, a save goes out with write bounds and only for the current workspace, and a link
goes to the browser only as an `http`, `https` or `mailto` address (a `file:` link opens as a file,
within the same bounds). The user's own `:e`, `:w` and dialogs are unbounded.

In the code: `tui/files.go`, `tui/outside.go`, `tui/htmlpane.go`

## Who an invocation acts for

Work that a call starts often runs later: a load or save job, a conflict's re-save, a key pressed
long after it was bound. So who an invocation acts for (the user, or a gated script with the
workspace current when it began) is taken once, when it begins, and carried with all of that work.

- Only the user's own input creates the user's authority. Work that has lost its authority is
  refused, never taken for the user's, and tests make that a panic.
- A queued gated write whose workspace the TUI has left is dropped, not aimed at the new one.

In the code: `tui/authority.go`, `tui/files.go`, `tui/scripting.go`, `script/editor.go`

## Script events

`autodoc.on(event, fn)` lets a script react to the page: `open`, `save`, `mode` and `change`.
Handlers run in the order they were added, as a callback runs, and a failing one is a notification
while the rest still run. `change` waits for a pause after the last edit (300 ms), and a handler's
own edits do not emit it again. A reload drops every handler with its engine.

In the code: `script/events.go`, `tui/luaevents.go`

## Script keys and menu rows

`autodoc.map` binds a command line to a Normal or Visual chord, or to a letter of the `SPC u` card;
`autodoc.menu` adds a row to the editor's right-click menu or the Plugins menu. A chord reaches the
script through a golib hook that runs only with nothing pending, so counts, operators and `f` keep
their meaning, and golib stays free of Lua. The keys and rows belong to the engine that made them,
and a reload drops them. A gated script's key or row checks `commands` when it runs.

In the code: `tui/luakeys.go`, `tui/menurows.go`, `tui/qml/views/LuaKeys.qml`

## KB types are git repos of files

A KB type is a git repo with a `kbtype.toml` (what the type is, its folders and their purposes, the
workspace settings it proposes) and a `tree/` that is the KB as it is created. Adding a new type,
or changing one, is a push to its repo, not an AutoDoc release.

Nothing in a type runs: its text files are read and copied. Because the URL can be anyone's, a type
is read within limits (text only, 256 KiB a file, 4 MiB and 1,000 files in all, no symlinks, no path
outside `tree/`), and a broken rule fails the whole type. `autodoc --check-kbtype` runs the same
check, for a type's own CI.

In the code: `core/kbtype`, `tui/kbtype.go`, `app/app.go`

## The daemon writes the scaffold, create-only

The app clones the type and plans its files, and the daemon writes them through
`workspace.scaffold`. The verb names a workspace and relative paths, and writes through that
workspace's own rooted filesystem, so no `..` or symlinked folder reaches outside it. Every file is
created exclusively: one that exists is kept, even if it appeared after the plan. It does not ask
what the workspace indexes, so a type's schema file is created under an include that only admits
Markdown.

- At most 64 files a call. A file's refusal is its result, so a failure part way reports what
  landed. Nothing is rolled back, and applying again creates only what is missing.
- The order is fixed: add the workspace, read it back, write the files, then configure, so the
  schema the configure names exists.

In the code: `rpc/scaffold.go`, `internal/daemon/scaffold.go`, `tui/settings.go`

## Choosing a type stages it

Choosing a type in the Add-workspace dialog clones it into a stage. Every choice, and the dialog
opening or closing, invalidates the stage before it, so only the latest choice can be shown or
applied, and a stale one is removed unseen. Add takes the type as staged when it is pressed.

In the code: `tui/kbtype.go`, `tui/settings.go`

## The operations manual is AutoDoc's own

A type supplies `AGENTS.md`, an `ABOUT.md` per folder, templates and its frontmatter types, but
never `KB_OPERATIONS.md`. That file is embedded in AutoDoc and versioned with the build: an apply
writes it when it is missing and replaces a copy that declares an older `autodoc_version`. The
Neovim side reads the same file, so both write one text.

In the code: `agents.go`, `core/kbtype`, `internal/daemon/scaffold.go`
