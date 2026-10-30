# AutoDoc's architecture

How AutoDoc is put together, and the one convention every part of it follows. Read it before
adding a feature: a feature that follows it touches no central file. The decisions behind it are
[the module convention](decisions/contract.md#features-as-modules-with-verb-contracts) and
[the todo system](decisions/todos.md#the-daemon-owns-the-todo-system), its first module.

## 1. Processes

| Process | What it is | Starts as |
| --- | --- | --- |
| the daemon | owns the store, the workspaces' indexes, the todo stores; serves msgpack-RPC on a unix socket | `autodoc --serve`, or the first client |
| the TUI / GUI | the editor: a client of the daemon, at the daemon's exact protocol | `autodoc --ui`, `autodoc --gui` |
| `--call` | one verb, from a shell, JSON in and out | `autodoc --call <verb> '<params>'` |
| `--contracts` | every verb's contract, printed with no daemon | `autodoc --contracts [filter] [--format md]` |
| autodoc.nvim | Neovim's client (Lua) | the plugin |
| plugins | programs of their own, on their own protocol with the TUI | the Plugins menu |

The daemon never pushes: clients poll `sys.events` (and, for todos, `todos.changes`).

## 2. Packages

| Package | Job |
| --- | --- |
| `core/module` | the convention's types: a verb's contract (`Contract`) and typed handler (`Handle[P, R]`), public errors (`PublicError`), a `Module` |
| `core/todo` | the todo system: the format (versioned schema files), the rules and every write of a `.todo-list`; the `todos.*` verbs, as a `Module` |
| `core/schema` | frontmatter schemas: a workspace's `schema.yaml`, and the todo formats |
| `core/docs` | reads and writes files for AutoDoc's own apps, conditional on the version the writer read: versioned reads and writes of a workspace's files over a `vfs.FS` |
| `core/index` | one workspace's index in the store: its indexer, links, the link graph, search, and the writer port its documents are kept through; a path's mode (`Mode`): full, or graph-only (its links and relations, no chunks), and the todo store it lies in ([graph-only indexing](decisions/todos.md#graph-only-indexing)) |
| `core/store` | the one store: every workspace and its index, in one SQLite file, read and written through golib `dao` (one declaration per table), with the lease that makes one daemon its only server; migrations, its tables (workspaces, preferences, and the metered services: embedding providers and rankers, keys sealed, calls metered), and the seam a module declares its own through |
| `sql/deployments` | the store's schema as numbered SQL scripts, applied by golib `dao/deploy` (see `docs/ops/schema-scripts.md`) |
| `core/todo/sqlitejournal` | the todo system's Journal on the store, its tables declared through the seam |
| `core/follow` | keeps a workspace's index following its root: watching and polling it |
| `core/kind`, `registrations`, `derived` | file kinds, chunkers, derivers |
| `core/workspace` | a workspace as it is added (`Spec`, `Normalize`, the default patterns), its glob grammar and matcher, and opening its root as a `golib/vfs` filesystem, with its include/exclude patterns |
| `core/config` | the daemon's own settings: reads `$XDG_CONFIG_HOME/autodoc/config.toml`, and resolves the socket, the state directory and the store (its XDG paths, resolved without touching the disk) |
| `core/pgstore`, `pgwriter` | a Postgres search store and the index's second writer (the enterprise destination) |
| `enterprise/config`, `ingest`, `embedder` | the enterprise search server (`--server`): `server.toml`, the `.view` ingesters (one writer per tenant), and the one central embedding worker |
| `core/paths`, `clock` | the path helpers and the one clock the core packages share |
| `core/outline`, `export`, `edition` | as named |
| `rpc` | the daemon's socket, the msgpack-RPC API: every verb registered from its contract, rpc's own and the modules'; `Catalog`. A `file.write` on a file a workspace indexes is that workspace's `doc.write` |
| `internal/daemon` | the daemon's workspaces: each served by an indexer and a follower, added, renamed and removed while it runs; each path's mode from the todo registry and the workspace's `graph_only` and `todo_search`; the todo system's disk and roots |
| `internal/client` | the daemon's client: the session (handshake, spawning `--serve`), restart handoffs |
| `internal/endpoint` | which socket serves a config's store |
| `app` | the binary's modes as one function, `app.Main`: `--serve` is the daemon, `--ui` the TUI, `--call` one verb as JSON, `--contracts` every verb's contract, …; the one module list; a main hands it its version ([one entry point](decisions/search.md#one-entry-point-for-every-build)) |
| `cmd/autodoc` | the binary, the community build: `app.Main` with no registrations |
| `tui` | the terminal UI and the GUI, their host and its features: search, the files, a Vim-keyed editor, the explorer's Links tab and the GUI's GlobeMap and Mindmap, the todos; its screen written in QML |
| `script` | the Lua engine for the editor's commands (gopher-lua), exportable to other programs; no AutoDoc imports |
| `plugin` | the plugin protocol and the SDK a plugin is written with ([plugins.md](plugins.md)) |
| `internal/contractsgen` | writes AGENTS.md's verb and error tables, and autodoc.nvim's `contracts.lua`, from the contracts |

## 3. Dependency rules

The rules, and the test that holds each, are [CONVENTIONS.md §2 and §3](../CONVENTIONS.md#2-packages-and-dependencies).
How they shape the tree:

- `core/*` is the domain, below every host. `core/module` is the convention's types, and nothing
  of AutoDoc's sits beneath it.
- Core reaches the disk only through golib's `vfs` (`core/docs` over a `vfs.FS`). `os` appears
  only where a `vfs/local` FS is built, outside `core/` (`internal/daemon`'s `TodoDisk`, `app`).
  The deviations still in core are listed with the rule.
- `rpc` is a host: it adapts modules to the wire. The TUI's script engine is another host of the
  same contracts.
- A port (an interface a package needs) is declared by the package that consumes it:
  `todo.Journal`, `todo.Filesystems`, `todo.Roots`, and rpc's `Workspace`, which
  `internal/daemon`'s served workspaces implement (`rpc.Static` is a fixed one, for tests).

## 4. The convention

### 4.1 A daemon feature is a core package plus a Module

```go
// core/<feature>/module.go
func (m *Manager) Name() string { return "todos" }
func (m *Manager) Verbs() []module.Verb {
	return []module.Verb{
		module.Handle(module.Contract{Verb: "todos.get", Since: 20, Audience: module.AudienceAgent,
			Result: "task", Errors: []int64{CodeNoTask}, Doc: "One task, with its rev and body."},
			func(ctx context.Context, p idParams) (Task, error) { return m.Get(ctx, p.At, p.ID) }),
	}
}
func (m *Manager) Errors() []module.PublicError { … }
```

- **Params come from the handler's struct:** the field order is the position, `param:"name"` the
  name, the Go type the param's type; a type implementing `module.Decoder` decodes itself.
- **Results are json-tagged structs**, carried by `module.Wire`.
- **Public errors carry their code once.** A module's codes sit in its range (`core/module`
  `codes.go`); −32060 to −32070 are the daemon's shared codes, which any module may answer.
- **Events:** `Contract.Event` names the kind, and the params holding the workspace and the
  detail; `Verb.Events` resolves them when they are inside a param or in the result.
- **`Name`, `Verbs` and `Errors` do no I/O**, so `--contracts` builds the modules with no
  dependencies.
- **Registration:** `app/serve.go`'s `modules(deps)` is the one list. `rpc.WithModules` serves
  them. The gate, `sys.events`, error mapping, `sys.capabilities`, `sys.contracts`, `--contracts`,
  AGENTS.md's tables and the Lua namespaces are all derived from the contracts.

### 4.2 A UI feature is a `Feature`

```go
// tui/<feature>.go
type todos struct{ c Core; … }
func (t *todos) Name() string                          { return "todos" }
func (t *todos) State() map[string]any                 { … } // its App.* sources
func (t *todos) Commands() map[string]Command          { … } // its App.* commands
// optional: Connected(), Started(), Entered(ws), Kinds()+Event(kind, ws, detail),
// PreferencesChanged(), Panels() []PanelSpec
```

- `Host.features()` lists them; the registry snapshots each feature's state once, merges sources
  and commands into the program, and refuses a name two features claim.
- **A feature sees `Core`, never `Host`:** set a source, open or close a dialog, notify, call
  the daemon off the loop (`Go`), a latest-answer-wins `Ticket`, open a file.
- Its QML is a component in `tui/qml/dialogs` or `views`, declared once in `main.qml`.
- **Panels come from each program's registry**, built from Features implementing `Panel`
  (`Panels() []PanelSpec`). A spec names its drawer and focus view, edge, places, defaults and
  percentage bounds. Duplicate ids/views and invalid geometry are refused before state is built;
  descriptors and place slices are snapshotted. Focus follows declared priority, with stable
  contribution order for ties. No package-global runtime panel list exists. The one exception is
  the plugins feature's card panels, which follow the plugins folder
  ([card panels made at runtime](decisions/plugins.md#card-panels-made-at-runtime)): the registry
  stages that contributor's new specs under the same checks, the document is reloaded with them
  (`ReloadWithSources` declares an added card's sources), and the change commits only when the
  reload succeeded.
- Panel callbacks receive a contributor-scoped `PanelHost`, not `*Host`. View calls, focus
  queries and terminal configuration are restricted to that feature's validated ids/views;
  cross-role id/view collisions are refused. Each contributor receives its own services even
  when feature labels match. Calls before view attachment report a precondition error.
  The scoped facade holds Core in a named field and forwards only panel/agent services;
  asserting it to full Core cannot recover unchecked view or daemon operations.
  Cross-panel toggles/open/close are navigation actions. Focus return uses
  `RestoreKeyboardPane`, limited to the editor and the registry's receiving panes, not arbitrary
  view methods. Form-text reset is `SetFormText`, distinct from ordinary source updates.
  Explorer and relations contribute their own panel specs. Relations owns its open hook,
  including the page's graph refresh; no links-refresh operation remains on `PanelHost`.
  `terminals` owns startup and
  focus restoration; `agents` owns its profile form/model and running identity. Their state and
  commands are not part of `legacy`. The agent panel is one contribution, its agents tabs inside
  it (agenttabs.go), not one panel spec per agent; the terminal's shells are tabs too. Both
  strips' + and tab menus come from one helper (tabmenus.go) over `TerminalPanel.Menus`.
  Their `Catalog()` entries own terminal toggle/open (`:terminal` / `:term`), agent toggle,
  and profile settings, so catalog invocations and their QML twins reach the same Feature.
- `PreferencesChanged()` projects each feature's current preferences into its own sources and
  choosers. Geometry/float readback uses the program's validated panel schema; standalone
  preference readers use fresh built-in descriptors without runtime callbacks.
- Terminal and agent keys are declared through their `Prefs()` rows and written through
  `Core.SetPref`; no feature supplies an arbitrary whole-prefs mutation callback. Edge rows
  publish their geometry and chooser sources through `State`, and profile/default rows refresh
  their model through `Changed`. JSON profiles stay raw `ValuePref` strings and are decoded
  inside the agents feature, not kept as a second mutable profile list in Host preferences.
  A profile's persisted codename is its delivery identity; legacy names derive a lower-case,
  hyphen-separated default, with deterministic suffixes allocated in stored order for collisions.
  The first list save persists all identities; display-name edits do not renumber them. Display
  labels may repeat: editing, default and switch flows resolve by codename. A running identity
  stays reserved after profile removal; a refused stop request does not release it. The default
  preference stores the codename. At load, absence of the canonical key migrates the legacy name
  to its first stored matching profile and persists that identity once. An existing canonical key,
  including an empty value, never falls back to the old name. The old key is only a compatibility
  mirror, not a lookup key after migration.
- Today's host is the `legacy` feature; features move out of it one file at a time (follow-up
  2/7).
- **Two panels may open at one edge** (the explorer and the todos at the left, by default): each
  is its own drawer over the page, the one opened later drawn over the other; Ctrl+h/j/k/l from
  the page goes to the first by declared priority. No panel moves or resizes another.
- **One choice among a few, always in view, is a `ComboBox`** (the todos' store, Fonts' faces):
  its rows a `ListModel`, its `currentIndex` a source, `onActivated` the command. A list to
  filter is a picker.
- **A list to filter and choose from is a picker, never a fifth layout.**
  `tui/qml/dialogs/Picker.qml` is the one layout: a field over a list, the item under the cursor
  previewed beside it. A use sets the Dialog's `title` and its declared properties: `name` and
  `model` (required), `label`, `fieldText`, `placeholder`, `note`/`noteShown`, `listTitle`,
  `previewTitle`/`previewText`/`previewAt`, `acceptText`, and `widthPercent`/`heightPercent` (the most of
  the Window it takes; a short list, such as the todos' Status…, asks for less). Its `TableViewColumn`s go into the
  list. Its handlers call `App.pickFilter`, `pickMove`, `pickChoose`, `pickAccept` and
  `pickCancel` with the `name`, and Go finds the picker by it. A feature offers its pickers by
  name from an optional `Picks()`; the registry merges them, refusing a name two features claim,
  and serves the `App.pick*` commands over them. That Go side is a `pick[T]` (`tui/pick.go`):
  - it holds every item, and filters them into a `rows[T]`;
  - it previews the row under the cursor off the loop, the latest asked for winning;
  - `choose` runs once it has closed the dialog, and `cancel` when it is dismissed;
  - `enter`, `submit` and `query` cover a picker that is not a plain choice: the draft's name, or
    the search.

  To make a picker, return a `pick[T]` from the feature's `Picks()`, its `App.<name>Preview*`
  sources (and a field source, when `open` should empty the field) from its `State()`, and add a
  `Picker { name: "…" }` use: a component file of its own, like `FileOpen.qml`, or inline in
  `main.qml`. `open()` shows it.
  `SearchPicker.qml` keeps its own layout for its stage row, but its handlers are the same.

### 4.3 Core package rules

The rules for a core package are [CONVENTIONS.md §3](../CONVENTIONS.md#3-core-packages). A timed
wait's `NOTE:` names why it must be timed: backoff, nothing can announce it, a named deadline, a
grace period, batching, debounce. Tests may sleep.

### 4.4 Tests

The test rules are [CONVENTIONS.md §9](../CONVENTIONS.md#9-tests). Where each one shows in this tree:

- a test-only export from `export_test.go`: index's `BufferedWriter`;
- a production hook as a nil package variable, set only by tests: index's `commitFailed`,
  pgstore's `putHook`;
- a failure injected at one statement, white-box: `failWrites`, `renamed`;
- a contract several implementations meet, as a `<pkg>test` package: `index/writertest`, golib
  `search/searchtest`, `pgstore/pgtest`, `todotest`;
- a real disk only where it is the point: versions, symlinks, the daemon's `/`.

## 5. Store and migrations

One SQLite file (`autodoc.db`), one writer connection. Migrations are
`sql/deployments/sqlite/0000NN_update_*.sql` with their `_revert_` pairs, listed in
`docs/ops/schema-scripts.md`. The migration rules are [CONVENTIONS.md §8](../CONVENTIONS.md#8-storage-and-migrations).

A module declares its own tables through the store's seam, `store.Declare` (`core/store/declare.go`):
at its package's init, the module names a build function for its dao schemas. Every store builds
every declaration when it opens, for its writer and its readers, and the list is frozen then. The
tables themselves are the migrations'. The todo journal (`core/todo/sqlitejournal`) is the first to
declare its tables this way. That package joins core/todo and core/store, so neither imports the
other.

## 6. Protocol and versioning

- `rpc.Protocol` is the newest protocol and `rpc.MinProtocol` the oldest served. A verb arrives at
  a protocol: its contract's `Since`.
- The protocol rules, and what follows a new verb, are
  [CONVENTIONS.md §4](../CONVENTIONS.md#4-daemon-features-and-the-rpc-protocol).

## 7. Concurrency

- **The daemon:** golib serves each request on its own goroutine. A session's first requests are
  serialized behind its `sys.hello`: the gate, on the connection's read loop, waits for a hello still
  being answered (`rpc/hello.go`). The todo system holds one
  mutex per store, and SQLite's single writer serializes the store.
- **The TUI:** one loop. Daemon calls run off it (`Core.Go`) and come back on it; an answer
  that is no longer the latest is dropped (`Ticket`).
- **Lua:** scripts run on the loop under a deadline, and their daemon calls are asynchronous.

## 8. Testing gates

The checks to run before pushing, and what CI runs (on each merge into `canary` and on the milestone
pull request into `main`), are in [CONTRIBUTING.md](../CONTRIBUTING.md#running-the-checks).
Never `go test -tags gui ./app/`: it opens a window.
