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
| `core/index` | the indexer, links, graph, search |
| `core/store` | the SQLite store: lease, migrations, the tables, the todo journal |
| `core/follow` | watching and polling a workspace |
| `core/kind`, `registrations`, `derived` | file kinds, chunkers, derivers |
| `core/config`, `workspace`, `outline`, `export`, `edition`, `pgstore` | as named |
| `rpc` | the daemon's socket: registers every verb from its contract; `Catalog` |
| `internal/daemon` | the served workspaces; the todo system's disk and roots |
| `internal/client` | the daemon's client: the session (handshake, spawning `--serve`), restart handoffs |
| `internal/endpoint` | which socket serves a config's store |
| `app` | the modes (`--serve`, `--ui`, `--call`, `--contracts`, …) and the one module list |
| `tui` | the TUI/GUI host and its features |
| `script` | the exportable Lua engine (gopher-lua); no AutoDoc imports |
| `plugin` | the plugin protocol |
| `internal/contractsgen` | writes AGENTS.md's verb and error tables from the contracts |

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
  `todo.Journal`, `todo.Filesystems`, `todo.Roots`.

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

### 4.3 Core package rules

| Rule | Form |
| --- | --- |
| package | one concept; `doc.go` (what, concurrency claim) and `README.md` |
| constructor | `New(required…, opts ...Option)`; ctx first on any I/O |
| I/O | golib `vfs` only (§3) |
| time | `WithClock`; no `time.Now()` in logic |
| logging | `WithLogger(logger.Logger)` |
| errors | `errs.Sentinel`; wrap with `%w`; public ones in the module's `Errors()` |
| wire types | json-tagged structs in the package |
| tests | black-box `<pkg>_test` for the surface; golib `vfs/memfs` for disks |

## 5. Store and migrations

One SQLite file (`autodoc.db`), one writer connection. Migrations are
`sql/deployments/sqlite/0000NN_update_*.sql` with their `_revert_` pairs, listed in
`docs/ops/schema-scripts.md`. A module's tables are declared in `core/store` today
(`todojournal.go`); a table-registration seam is follow-up 3/7.

## 6. Protocol and versioning

- `rpc.Protocol` is the newest protocol and `rpc.MinProtocol` the oldest served. A session keeps
  the protocol it declared.
- A verb arrives at a protocol: its contract's `Since`.
- **Adding a verb** is its contract plus its handler. `rpc/testdata/contracts.json` (the golden
  file; `go test ./rpc -update` rewrites it) and AGENTS.md (`go generate ./rpc`) follow, and
  tests fail until they do.
- A change that is not an addition raises `MinProtocol`, or keeps the old behaviour below it.

## 7. Concurrency

- **The daemon:** golib serves each request on its own goroutine. The todo system holds one
  mutex per store, and SQLite's single writer serializes the store.
- **The TUI:** one loop. Daemon calls run off it (`Core.Go`) and come back on it; an answer
  that is no longer the latest is dropped (`Ticket`).
- **Lua:** scripts run on the loop under a deadline, and their daemon calls are asynchronous.

## 8. Testing gates

`gofmt -l`, `go vet ./...`, `go vet -tags gui ./tui/ ./app/`, `go test ./...`, and `go test
-tags gui ./tui/`. Never `go test -tags gui ./app/`: it opens a window. CI also runs `-race`, and a
coverage policy: no file drops more than 1 point.
