# core/store

AutoDoc's one store: every workspace, its settings, and every workspace's index, in one SQLite
file (`autodoc.db`). The daemon opens it once and hands it to the packages that keep rows in it.

The schema is `sql/deployments`, applied by golib's `dao/deploy` when the store opens; the
released scripts are listed in `docs/ops/schema-scripts.md`. Every row is read and written
through golib's `dao`, one declaration per table. The store declares its own tables
(`tables.go`); every other package declares its own through the seam (`Declare`). A workspace's
tables are reached only through its `Scope`, which puts the workspace on every statement.

```go
import "github.com/yongjohnlee80/autodoc/core/store"

db, err := store.Open(ctx, "/path/autodoc.db", store.WithClock(now))
defer db.Close()
err = db.Write(ctx, func(tx *store.Tx) error { … })
err = db.Read(ctx, func(tx *store.Tx) error { … })
```

## Opening and closing

| Name | What |
| --- | --- |
| `Open(ctx, path, opts…)` | takes the lease, opens the writer and the readers, applies the pending scripts |
| `WithClock(c)` | the clock the store's recorded times read (`updated_at`, an event's time); default `time.Now` |
| `Schema()` | what the update at `Open` did: the scripts applied, and any warnings (`deploy.Status`) |
| `Clock()` | the store's clock, which the daemon hands every workspace's indexer |
| `Path()`, `FileSize()` | the store's file, and its size without the write-ahead log |
| `Close()` | closes both pools and releases the lease |

## One writer, many readers

The store opens two pools on one file, in WAL mode:

- **The writer:** a pool of one connection, whose transactions begin `IMMEDIATE`. `Write(ctx,
  fn)` runs `fn` in one write transaction, committed when `fn` returns nil and rolled back
  otherwise.
- **The readers:** one connection per CPU, each set `query_only`. `Read(ctx, fn)` runs `fn` in one
  read transaction: every read in it sees one commit.

`fn` receives a `Tx`. The store's own methods (workspaces, providers, preferences, …) each run one
such transaction. Another package reaches its rows through the `Tx` with `Scoped`, `On`,
`NewBatch` or its declared tables' `In`.

## Workspaces and their scope

`Workspace(id)` is a workspace's `Scope`. Each accessor (`Self`, `Connections`, `Patterns`,
`Models`, and `Scoped` for a package's own table) returns a DAO with a dao hook on every
statement. An insert or upsert has `workspace_id` set; every other statement has `workspace_id =
<id>` added to its WHERE. A `Batch` (`NewBatch`, `PatternBatch`) stamps each row it adds. Joins
between two workspace-owned tables go through `InnerJoin` and `LeftJoin`, which join on the
workspace as well as the key. `WorkspaceColumn` is the column every such table keys on first.

The workspace rows and their settings:

- `AddWorkspace`, `Workspaces`, `RenameWorkspace`, `RemoveWorkspace`; `ErrTaken`,
  `ErrNoWorkspace`.
- One setting at a time: `SetWorkspacePatterns`, `SetWorkspaceExclude`,
  `SetWorkspaceEmbeddingPolicy`, `SetWorkspaceSectionTokens`, `SetWorkspaceTextExtensions`,
  `SetWorkspaceProvider`, `SetWorkspaceSchema`, and their readers (`EmbeddingPolicy`,
  `SectionTokens`, `TextExtensions`, `WorkspaceProvider`, `SchemaPath`, `Retrieval`).
- `Configure(ctx, id, Changes)`: every field of an edit checked first, then all of them written in
  one transaction. A refused field is `ErrSetting`, with a `SettingError` saying why.
- A workspace's databases (`connections.go`): its source and its destination, at most one of
  each role. `Connection` opens the sealed DSN, for the daemon; `Connections` gives a client a
  `ConnectionSummary` (`Summarize`), never the DSN.

## Declared tables: the seam

A package declares its own tables with `Declare(name, build)` from a package-level variable, so
the call runs at the package's init. `build` makes the package's dao schemas on a connection.
Every store calls every `build` when it opens, once for its writer and once for its readers. The
first `Open` freezes the list: a `Declare` after it, or a second one of the same name, panics. The
index (`core/index`, `"index"`) and the todo journal (`core/todo/sqlitejournal`, `"todo"`)
declare theirs this way. The tables themselves are created by the migrations, not by `Declare`.

Inside a transaction, `Declared[T].In(tx)` is the package's tables for that transaction's
connection. `Scoped(sc, schema, tx)` is a declared table's DAO scoped to a workspace,
`NewBatch(sc, schema, tx)` a scoped batch insert, and `On(schema, tx)` a DAO with no workspace,
for a table no workspace owns. `Col`, `Joined` and `NoText` help build the declarations.

## The lease

`Open` first takes an exclusive, non-blocking flock on a file beside the store,
`.autodoc-lease-<dev>-<inode>`. The name follows the database, not the spelling of its path, so a
symlink or a hardlink to the store is the same store. A lease another process holds is `ErrBusy`.
The lease file is never the store itself: closing a descriptor to the store would drop SQLite's
own locks.

The daemon holding the lease records itself beside it with `WriteLeaseInfo` (written to a temporary
file, then renamed), so a client started with another config can find it: `ReadLeaseInfo`,
`RemoveLeaseInfo` (only the record of the given instance), and `Identity` (the store's device and
inode, reported as `store_id`).

## The keyslot

The store's key is a file beside it, `<store>.key`: 32 random bytes, mode 0600, made the first time
a secret is sealed. A key file others can read is refused (`ErrKeyslotExposed`). A secret is
AES-256-GCM, with its additional data naming what it is and whose row it belongs to, so a sealed
value copied to another row does not open (`ErrSealed`). The store seals an embedding provider's
and a ranker's API key, and a workspace connection's DSN. A store copied without its key file
opens none of them.

## Embedding providers and rankers

- **Providers** (`providers.go`): `AddProvider`, `UpdateProvider`, `RemoveProvider`, `Providers`
  (a `ProviderInfo` says only whether there is a key), and `ProviderWithKey` (the opened key, for
  the daemon). Kinds `KindOllama`, `KindOllamaCloud`, `KindOpenAI`. The provider in use is the
  preference `PrefProvider`. `RemoveProviderInUse` also takes the provider's models out of use in
  every workspace, in the same transaction, and returns them (`Retired`) to be reclaimed.
- **Rankers** (`rankers.go`, ADR 0215): `AddRanker`, `UpdateRanker`, `RemoveRanker`, `Rankers`,
  `RankerWithKey`; kinds `KindTEI`, `KindRerankAPI`; the ranker in use and its window are the
  preferences `PrefRanker` and `PrefRankerWindow`.

## Metered services

Each provider and each ranker has its calls metered in two tables of the same shape: usage by day
(`embedding_usage`, `ranker_usage`) and a log of recent calls (`embedding_log`, `ranker_log`),
which keeps the last 200 per provider or ranker. `RecordCalls` and `RecordRankerCalls` add a batch
of `CallRecord`s in one transaction. `ProviderUsage`, `ProviderLog`, `RankerUsage` and `RankerLog`
read them back, the latest first.

## Preferences and events

- **Preferences:** the store's, not a workspace's. `Preferences`, `SetPreference`. `Owned` names
  the ones only their own verb may set.
- **Events:** the daemon-wide log of configuration and lifecycle changes. `AppendEvent` adds one
  and keeps the last `EventsKept` (1000). `Events(ctx, since, limit)` pages the log after a cursor.
  A cursor outside the retained log is `ErrEventsExpired`.

## Compaction

A delete puts SQLite's freed pages on its freelist; the file does not shrink.

- `Compact` puts the store in incremental auto-vacuum mode and rewrites it with `VACUUM`. It
  refuses with `ErrNoRoom`, before anything runs, when the file system has less than twice the
  store's size free. It returns the sizes before and after (`Compaction`).
- `IncrementalVacuum` returns the freelist to the file system once the store is in that mode.
  The index calls it after a reclaim.
- `Checkpoint` copies the write-ahead log into the store and empties it, for a store no daemon
  serves (`--compact`).
- `AutoVacuum` and `FreePages` report the mode and the freelist's size.

These run outside any transaction, on the writer's connection: writes wait for them, and reads go
on from their snapshots.

## Concurrency

A `Store` is safe for concurrent use, except `Close`, which must be its last call.

- Writes are serialized by the one writer connection: a `Write` waits for the one holding it.
  Reads run in parallel, up to one connection per CPU, and do not wait for writes.
- A `Tx` belongs to the function it is passed to and ends when that function returns.
- Inside a `Write`'s function, do not call a method that uses the writer connection: one that
  writes, or `AutoVacuum`, `FreePages`, `IncrementalVacuum`, `Checkpoint`, `Compact`. The outer
  `Write` holds the connection, so the call waits until its ctx ends.
- `Declare` is safe from any goroutine, and panics once a store has opened.
- Between processes, the lease lets one open a store at a time.
