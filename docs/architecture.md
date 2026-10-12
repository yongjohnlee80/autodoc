# AutoDoc's architecture

How AutoDoc is put together, and the one convention every part of it follows. Read it before
adding a feature: a feature that follows it touches no central file. The decisions behind it are
ADR 1791719881 (the module convention) and ADR 1791532020 (the todo system, its first module).

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
| `core/module` | the convention's types: a verb's `Contract`, `Handle[P, R]`, `PublicError`, `Module` |
| `core/todo` | the todo system: format (versioned schema files), rules, every write; a `Module` |
| `core/schema` | frontmatter schemas: the KB's `schema.yaml` and the todo formats |
| `core/docs` | versioned, conditional reads and writes of a workspace's files over a `vfs.FS` |
| `core/index` | the indexer, links, graph, search, and the writer port its documents are kept through |
| `core/store` | the SQLite store: lease, migrations, its tables (workspaces, preferences, and the metered services: embedding providers and rankers, keys sealed, calls metered), and the seam a module declares its own through |
| `core/todo/sqlitejournal` | the todo system's Journal on the store, its tables declared through the seam |
| `core/follow` | watching and polling a workspace |
| `core/kind`, `registrations`, `derived` | file kinds, chunkers, derivers |
| `core/workspace` | a workspace as it is added (`Spec`, `Normalize`, the default patterns), its glob grammar and matcher, and opening its root |
| `core/config` | the daemon's own settings (`config.toml`) and its XDG paths, resolved without touching the disk |
| `core/pgstore`, `pgwriter` | a Postgres search store and the index's second writer (the enterprise destination) |
| `core/paths`, `clock` | the path helpers and the one clock the core packages share |
| `core/outline`, `export`, `edition` | as named |
| `rpc` | the daemon's socket: registers every verb from its contract; `Catalog`. A `file.write` on a file a workspace indexes is that workspace's `doc.write` |
| `internal/daemon` | the served workspaces; the todo system's disk and roots |
| `internal/client` | the daemon's client: the session (handshake, spawning `--serve`), restart handoffs |
| `internal/endpoint` | which socket serves a config's store |
| `app` | the modes (`--serve`, `--ui`, `--call`, `--contracts`, …) and the one module list |
| `tui` | the TUI/GUI host and its features |
| `script` | the exportable Lua engine (gopher-lua); no AutoDoc imports |
| `plugin` | the plugin protocol |
| `internal/contractsgen` | writes AGENTS.md's verb and error tables, and autodoc.nvim's `contracts.lua`, from the contracts |

## 3. Dependency rules

- `core/*` never imports `rpc`, `app`, `tui` or `internal`. `core/module` imports nothing of
  AutoDoc's.
- **No direct I/O in `core/`.** Every filesystem operation goes through golib's `vfs`
  (`core/docs` over a `vfs.FS`). `os` appears only where a `vfs/local` FS is built, outside
  `core/` (`internal/daemon`'s `TodoDisk`, `app`). `core/store`'s SQLite plumbing is the
  exception being reviewed (follow-up 3/7).
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
func (t *todos) Commands() map[string]decl.HandlerFunc { … } // its App.* commands
// optional: Connected(), Started(), Entered(ws), Kinds()+Event(kind, ws, detail)
```

- `Host.features()` lists them; the registry merges their sources and commands into the program
  and refuses a name two features claim.
- **A feature sees `Core`, never `Host`:** set a source, open or close a dialog, notify, call
  the daemon off the loop (`Go`), a latest-answer-wins `Ticket`, open a file.
- Its QML is a component in `tui/qml/dialogs` or `views`, declared once in `main.qml`.
- **Docked panels come from one registry** (`panelSpecs` in `panels.go`): a drawer's id, the view
  that takes the keyboard, its edge, the edges whose sizes are kept.
- Today's host is the `legacy` feature; features move out of it one file at a time (follow-up
  2/7).
- **A list to filter and choose from is a picker, never a fifth layout.**
  `tui/qml/dialogs/Picker.qml` is the one layout: a field over a list, the item under the cursor
  previewed beside it. A use sets the Dialog's `title` and its declared properties: `name` and
  `model` (required), `label`, `fieldText`, `placeholder`, `note`/`noteShown`, `listTitle`,
  `previewTitle`/`previewText`/`previewAt` and `acceptText`. Its `TableViewColumn`s go into the
  list. Its handlers call `App.pickFilter`, `pickMove`, `pickChoose`, `pickAccept` and
  `pickCancel` with the `name`, and Go finds the picker by it. That Go side is a `pick[T]`
  (`tui/pick.go`):
  - it holds every item, and filters them into a `rows[T]`;
  - it previews the row under the cursor off the loop, the latest asked for winning;
  - `choose` runs once it has closed the dialog, and `cancel` when it is dismissed;
  - `enter`, `submit` and `query` cover a picker that is not a plain choice: the draft's name, or
    the search.

  To make a picker, add a `pick[T]` to `h.picks`, its `App.<name>Preview*` sources (and a field
  source, when `open` should empty the field) to the state, and a `Picker { name: "…" }` use: a
  component file of its own, like `FileOpen.qml`, or inline in `main.qml`. `open()` shows it.
  `SearchPicker.qml` keeps its own layout for its stage row, but its handlers are the same.

### 4.3 Core package rules

| Rule | Form |
| --- | --- |
| package | one concept; `doc.go` (what, concurrency claim) and `README.md` |
| constructor | `New(required…, opts ...Option)`; ctx first on any I/O |
| I/O | golib `vfs` only (§3) |
| time | `WithClock`; no `time.Now()` in logic |
| waits | no `time.Sleep` in production code: wait on the event (a channel, a context), or on a cancellable timer; every timed wait (`clock.Sleep`, `time.After`, a timer, a ticker) has a `NOTE:` directly above saying why it must be timed (backoff, nothing can announce it, a named deadline, a grace period, batching, debounce). `core/clock/waits_test.go` enforces both; tests may sleep |
| logging | `WithLogger(logger.Logger)` |
| errors | `errs.Sentinel`; wrap with `%w`; public ones in the module's `Errors()` |
| wire types | json-tagged structs in the package |
| tests | the surface black-box (`<pkg>_test`), internals white-box; see §4.4 |

### 4.4 Tests

- **The surface is tested from outside.** A test of what callers use (exported types and
  functions, verbs, a conformance contract) is in the black-box package `<pkg>_test`, so it can
  only do what a caller can.
- **Internals are tested inside.** A test that must reach unexported state is in the package
  itself: a loop's queue, a failure injected at one statement (`failWrites`, `renamed`), a row
  read back with SQL of its own.
- **Test-only exports go in `export_test.go`.** A black-box test that needs one internal piece
  gets it from an `export_test.go` in the package, which only tests compile (index's
  `BufferedWriter`). It is never production API.
- **A hook in production code is a nil package variable.** It is set only by a test, before
  anything runs, and its comment says so (index's `commitFailed`, pgstore's `putHook`).
- **A contract several implementations meet is a `<pkg>test` package.** Each implementation runs
  the same cells (`index/writertest`, golib `search/searchtest`, `pgstore/pgtest`, `todotest`).
- **Disks are golib `vfs/memfs`.** A disk is `vfs/local` over `t.TempDir()` only where the real
  file system is the point (versions, symlinks, the daemon's `/`).
- **No mass moves.** An existing white-box test that uses only the exported API moves to
  `<pkg>_test` when its file is next changed for another reason.

## 5. Store and migrations

One SQLite file (`autodoc.db`), one writer connection. Migrations are
`sql/deployments/sqlite/0000NN_update_*.sql` with their `_revert_` pairs, listed in
`docs/ops/schema-scripts.md`. Released scripts never change: their digests are pinned in tests.

A module declares its own tables through the store's seam, `store.Declare` (`core/store/declare.go`):
at its package's init, the module names a build function for its dao schemas. Every store builds
every declaration when it opens, for its writer and its readers, and the list is frozen then. The
tables themselves are the migrations'. The todo journal (`core/todo/sqlitejournal`) is the first to
declare its tables this way. That package joins core/todo and core/store, so neither imports the
other.

## 6. Protocol and versioning

- `rpc.Protocol` is the newest protocol and `rpc.MinProtocol` the oldest served. A session keeps
  the protocol it declared.
- A verb arrives at a protocol: its contract's `Since`.
- **Adding a verb** is its contract plus its handler. `rpc/testdata/contracts.json` (the golden
  file; `go test ./rpc -update` rewrites it), AGENTS.md and autodoc.nvim's
  `lua/autodoc/contracts.lua` (`go generate ./rpc`) follow, and tests fail until they do.
- A change that is not an addition raises `MinProtocol`, or keeps the old behaviour below it.

## 7. Concurrency

- **The daemon:** golib serves each request on its own goroutine. A session's first requests are
  serialized behind its `sys.hello`: the gate, on the connection's read loop, waits for a hello still
  being answered (`rpc/hello.go`). The todo system holds one
  mutex per store, and SQLite's single writer serializes the store.
- **The TUI:** one loop. Daemon calls run off it (`Core.Go`) and come back on it; an answer
  that is no longer the latest is dropped (`Ticket`).
- **Lua:** scripts run on the loop under a deadline, and their daemon calls are asynchronous.

## 8. Testing gates

`gofmt -l`, `go vet ./...`, `go vet -tags gui ./tui/ ./app/`, `go test ./...`, and `go test
-tags gui ./tui/`. Never `go test -tags gui ./app/`: it opens a window. CI also runs `-race`, and a
coverage policy: no file drops more than 1 point.
