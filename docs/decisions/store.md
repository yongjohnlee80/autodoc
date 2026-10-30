# Store decisions

The store is where AutoDoc keeps every workspace, its index (documents, chunks, links and vectors),
its settings and its secrets: one SQLite file by default, with PostgreSQL for the server component
and for a workspace's Postgres destination. This page records the decisions that shape it and what
each one means in the code. The scripts themselves are described in
[the store's schema](../ops/schema-scripts.md).

## A PostgreSQL engine for the store

The store's migration scripts come in one directory per engine, `sqlite/` and `postgres/`, with the
same script names. `postgres/000001` creates in one script the schema that SQLite's 000001 to
000025 leave, so `postgres/000002` to `000025` are comments. From 000026 on, every script ships for
both engines, or as a comment on the engine it has no work on, saying why.

- The translation: integer keys become `bigint` identities, `BLOB` becomes `bytea`, text is
  `COLLATE "C"` (byte order, as SQLite compares, which path ranges rely on), and FTS5's
  `chunk_fts` becomes `chunk.tsv`, a generated, weighted `tsvector` with a GIN index.
- SQLite stays every AutoDoc's default. PostgreSQL is the server component's first. Opening the
  store on PostgreSQL, searching it, sharing it between processes and copying a SQLite store into
  it are later steps.
- A parity test applies the PostgreSQL scripts and requires SQLite's tables with the same columns
  each; `chunk.tsv` is the one named difference.

In the code: `sql/deployments/deployments.go`, `sql/deployments/postgres/`,
`sql/deployments/deployments_test.go`

## Workspace settings saved together

A workspace's settings are edited in one dialog with two tabs. Edit is what the workspace is and
which files it holds (name, root, patterns, file types, frontmatter schema). Advanced is how it is
indexed and embedded and, where the edition offers them, its databases.

Save sends only what changed, in one `workspace.configure`. The daemon checks every change as its
single-field verb would, saves them all in one store transaction, then does what each change needs
(serve under a new name, restart with new patterns, re-index for a new section size). A refused
change leaves every setting as it was, and the dialog opens again as it was typed. The single-field
verbs stay, for scripts and older clients.

In the code: `internal/daemon/configure.go`, `rpc/configure.go`, `tui/settings.go`,
`tui/qml/dialogs/WorkspaceSettings.qml`

## Databases attach to a workspace

A workspace is still a directory. It may also have a source database, where its `.view` files run,
and a destination, where its index and vectors live: the local store by default, or PostgreSQL with
pgvector. Each workspace has at most one connection of each role, in `workspace_connection`.

- A connection's DSN is sealed by the store's keyslot and bound to the workspace and the role. A
  client sees a summary (engine, host, database, user, schema, whether a password is set), never
  the DSN.
- A Postgres destination is keyed by the workspace's `uid`, which is stable across renames and
  unique across machines, never by the local id.
- The database features sit behind one switch, `edition.Databases`, reported by
  `sys.capabilities`. When it is off, the daemon refuses those settings and the TUI hides them.

In the code: `core/store/connections.go`, `core/edition/`, `rpc/workspaceport.go`, `rpc/server.go`

## A keyslot seals the store's secrets

Secrets the store keeps (an embedding provider's API key, a database DSN) are sealed with the
store's own key. The key is a file beside the store, `<store>.key`: 32 random bytes, mode 0600, made
the first time a secret is sealed. The store refuses to use it when anyone but its owner can read
it.

A secret is AES-256-GCM, a 12-byte nonce followed by the ciphertext. Its additional data names what
the secret is and whose it is, so a sealed value copied to another row opens nowhere. A copied store
without its key file opens no secret.

In the code: `core/store/keyslot.go`, `core/store/connections.go`

## One Postgres destination store

AutoDoc has exactly one PostgreSQL index implementation, `core/pgstore`, with its own scripts in
`core/pgstore/postgres/` and its own ledger, `autodoc_schema`. Its tenant is the workspace `uid`,
first in every key, carried by every join and filtered in every statement.

Its columns follow what AutoDoc's indexer reads:

- **Kept:** each chunk's embed text, the document's indexer identity (stored verbatim) and source
  version, and each chunk's title and tags, so lexical ranking weighs title, breadcrumb, tags and
  body as SQLite's 10 : 5 : 5 : 1 does.
- **Dropped:** what serves only SQLite's mechanics: chunk generations (PostgreSQL's snapshots do
  that job), chunk ids, the commit watermark of the in-memory code index.
- **Deferred:** aliases, frontmatter diagnostics, name lookups and relations. The writer port lists
  them as `pgwriter.Deferred`, and a workspace that needs one may not be served by a Postgres
  destination until it is kept.

Lexical search uses `rag_english`, Snowball English stemming with no stop words, so every query word
stays required, as it does in SQLite. Semantic search scans exactly; an approximate index is a
measured follow-up.

In the code: `core/pgstore/`, `core/pgstore/pgwriter/pgwriter.go`, `core/pgstore/postgres/`

## A workspace keeps two models' vectors

A workspace keeps vectors for its active model and its target, and for no other model. The target is
the model its indexer is filling: the active one outside a switch, a new one during a switch. Both
are recorded on the `model` row, so a store-level sweep can read them for any workspace.

Every other model is reclaimed: its vectors are deleted in batches of 2,000, each its own write
transaction so other writes go on between them, then its row. Each batch first checks that the model
is still neither active nor the target, so a model switched back to keeps what is left. A reclaim
cut short leaves an inactive model with fewer vectors, which the next sweep finishes. Switching back
to a reclaimed model embeds it again from scratch.

In the code: `core/index/reclaim.go`, `core/index/semantic.go`

## When a model is reclaimed

- **A switch completes.** Once the flip to the new model is committed and published, the old model
  is no longer the target and is reclaimed. A query that began before the flip still reads the old
  vectors from its snapshot.
- **The target moves.** `setupModels` clears the target on a model it no longer fills, and that
  model is reclaimed unless it is the active one. This covers `embedding.cancel_switch` and a switch
  to a third model mid-switch: a cancelled switch loses its progress.
- A late batch of vectors for a model no longer active or targeted is dropped, never stored.

In the code: `core/index/semantic.go`, `core/index/reclaim.go`, `internal/daemon/embedding.go`

## The start sweep

When the daemon starts, after the workspaces are open, a sweep runs in the background across every
stored workspace, served or not, with or without a provider. It reclaims every model that is neither
active nor the recorded target. A workspace with an indexer is swept through it, after its
`setupModels` has recorded the target; one with no indexer is swept through the store. The sweep
runs while the daemon serves, and reports what it removed as a `models.reclaimed` notice.

In the code: `internal/daemon/reclaim.go`, `app/serve.go`, `tui/events.go`

## Removing a provider retires its model

Removing the provider in use (AI models, Remove, or `embedding.remove`) deletes its record and, in
the same store transaction, clears active and target on the model it served in every workspace,
unless another stored provider has the same kind and model name. Semantic search then goes off, and
the retired model's vectors are reclaimed in the background, through the store. A failed removal
changes nothing.

A removal with no provider in use takes the same path, so a model kept active for another provider
is retired with the last one. While another provider is in use, its model is left: it may still be
answering until a switch's flip. The confirmation says what the removal deletes.

In the code: `internal/daemon/embedding.go`, `core/store/providers.go`, `tui/providers.go`

## Purging refuses the active model and the target

`index.purge_model` reclaims an unused model at once, instead of at the next start. On the active
model it is refused with what to do instead (choose another model, or remove its provider); on a
switch's target it is refused with "cancel the switch", which reclaims it.

In the code: `core/index/semantic.go`

## The store compacts

SQLite puts the pages a delete frees on its freelist: the file does not shrink, and the rows kept
stay among the gaps. So, once every workspace's start sweep has succeeded, a store not yet in
incremental auto-vacuum mode is converted with one `VACUUM`, which also rewrites every table
contiguously. After that, each reclaim ends with `PRAGMA incremental_vacuum`, which returns its pages
to the file system.

- The conversion runs on the writer's connection, outside any transaction: writes wait, and reads
  go on from their WAL snapshots.
- It first requires free space of twice the store's file beside the store, and puts SQLite's
  temporary files there (`SQLITE_TMPDIR`), never in the system's temp directory. Without the room it
  is skipped, noticed, and retried at the next start.
- A start whose sweep failed for any workspace leaves the compaction to a later start, so no
  leftovers are kept out of the one rewrite.
- `autodoc --compact`, with the daemon stopped, reclaims every unused model and runs `VACUUM`
  again, for a store that switches models often.

In the code: `core/store/compact.go`, `internal/daemon/reclaim.go`, `app/compact.go`

## pgstore reclaims models too

In a Postgres store the same rule holds per tenant: embeddings of any model but the active one and
the target are deleted by `ReclaimModels`, in batches, each its own transaction that reads the two
models again. Its writer calls it after a flip, after a change of target, and at its start.
PostgreSQL's own autovacuum reclaims the space; AutoDoc compacts nothing there.

In the code: `core/pgstore/store.go`, `enterprise/embedder/embedder.go`

## Protocol 16 gates the deleting verbs

From protocol 16, `embedding.cancel_switch` deletes the partial target's vectors and
`embedding.remove` the removed provider's model's. An older client expects them to keep the vectors,
which the start sweep would delete anyway, so a session below 16 is refused those two verbs, saying
so. Every other verb serves it as before.

In the code: `rpc/args.go`, `rpc/server.go`

## Vectors follow their chunks

A workspace keeps a vector only while some chunk of it, alive or dead, has its text. A dead chunk
keeps its text's vector until gc deletes the chunk, and a text two documents share keeps it while
either has it. A text that comes back after its vector went is embedded again.

The writer keeps this in the transaction that would break it: gc and a document's removal delete the
vectors of the texts whose last chunk they deleted, and a batch of vectors stores none for a text no
chunk has (it was replaced while the provider embedded it). Each check is a lookup in the
`chunk_text` index on `chunk(workspace_id, text_hash)`.

In the code: `core/index/vectors.go`, `core/index/indexer.go`, `core/index/semantic.go`

## The orphan pass at start

What a store held before, or what a missed path left, is swept once a start, per workspace, after
its model sweep. A batch reads the next 2,000 vector keys after a cursor, in key order, looks their
texts up in `chunk_text`, and deletes those no chunk has. A batch's work is bounded by the keys it
examines, and a document committed meanwhile waits at most one batch. A workspace with an indexer is
swept through its writer; one without, through the store.

The store is compacted only once every workspace's model sweep and orphan pass have succeeded. The
pass logs `embedding.orphans.swept` with its count: after the first start, a count other than zero
means a path missed the rule.

In the code: `internal/daemon/reclaim.go`, `core/index/vectors.go`, `core/index/tables.go`

## Status is computed once per commit

`index.status` is computed in one read transaction, which reads the workspace's `commit_seq`, the
store's counts, the model's coverage and whether any document is not ready, and is kept under that
seq and model. Every writer transaction bumps `commit_seq`, so a later call at the same seq is
answered from one row. Clients that poll an idle workspace every second or two cost nothing more.

One computation runs at a time; a call that waited reads `commit_seq` again and computes if it has
moved, so no caller is answered for a superseded commit. The change log's head and oldest retained
seq, and what lives in memory, are read on every call.

In the code: `core/index/status.go`

## Readiness follows the batch

When a batch of vectors completes some documents, only those documents' texts are looked up, by key,
so the cost follows the batch and not the vectors the workspace holds. Past one lookup list (a very
large document), or for every document at once (a flip), the model's vectors are read once through
their index instead. The documents a batch touches are found through `chunk_text` with a plain
select, which SQLite plans through that index.

In the code: `core/index/semantic.go`
