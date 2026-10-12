# core/index

A workspace's derived index in the store (`core/store`): its documents, their chunks, full-text
and semantic search over the chunks, tags, aliases, facets, links and relations, and a change log.
The files are canonical. Everything here can be rebuilt from them.

The package has two halves. A `Store` is one workspace's index, read: listings, the change log,
the graph, status and lexical search. An `Indexer` keeps the `Store` current: it reads the
workspace's files, cuts them into chunks, writes them, and, with an embedding provider, fills
their vectors. Every row goes through the workspace's `store.Scope`, on tables this package
declares to the store, so the package holds no SQL.

```go
import "github.com/yongjohnlee80/autodoc/core/index"

s := index.NewStore(db, workspaceID) // db is the caller's *store.Store
ix := index.New(s, fsys, index.WithProvider(p), index.WithClock(db.Clock()), index.WithMatch(match))
go ix.Run(ctx)
ix.Touch("notes/a.md")
res, err := ix.Search(ctx, "pump seals", index.QueryOpts{Limit: 10})
```

## The Store

`NewStore(db, workspace)` is a workspace's index in an open `store.Store`. The store stays the
caller's: `Close` is a no-op. Each read runs in one read transaction, so it sees every document
at one generation.

| Area | Names |
| --- | --- |
| documents | `List` (path order, paged), `Documents` (with frontmatter, narrowed and sorted: `DocumentsOpts`), `Version`, `PathsUnder`, `Identity` |
| change log | `Changes(ctx, since, limit)`; a cursor older than the log keeps is `ErrCursorExpired` |
| graph | `Links`, `Backlinks`, `Neighborhood`, `Unresolved`; `ErrNoDocument` |
| frontmatter | `Diagnostics` |
| status | `Status`: the change log's bounds, counts, pending and failing jobs |
| search | `Search`: lexical only (the `Store` has no provider and no schema) |
| settings | `Retrieval`: the workspace's retrieval settings |

A document's chunks live under generations. A rewrite keeps the chunks whose hash is in the active
generation, adds the new ones under the next generation, and marks the rest dead at it. The
document's `active_gen` moves in the same transaction, so a reader sees the old generation or the
new one, never a mix. The writer deletes dead chunks when it is idle.

## The Indexer

`New(store, fsys, opts…)` builds the indexer for a `Store` over the workspace's root, a golib
`vfs.FS`. The options:

| Option | What |
| --- | --- |
| `WithWorkers(n)` | parallel readers and parsers (default NumCPU) |
| `WithBatching(size, delay)` | the writer commits up to `size` results (default 50), or after `delay` (default 200 ms) |
| `WithMatch(fn)` | which paths are the workspace's (its include and exclude); default all |
| `WithClock(c)` | the clock the recorded times read (the change log's age, a refused text's retry time) |
| `WithLogger(l)` | where it logs; default nowhere |
| `WithWorkspace(name)` | the workspace's name, for the logs |
| `WithSchema(fn)`, `WithTextExtensions(fn)` | the frontmatter schema and the plain-text extensions, asked once per document |
| `WithRegistrations(t)` | the build's chunkers and derivers (ADR 0216) |
| `WithProvider(p)` | embeds chunks for semantic search; without one, search is lexical only |
| `WithExternalEmbedding(onWork)` | the daemon runs the embedding work (`EmbedBatch`); `onWork` is told after each commit with documents |
| `WithSearcher(fn)` | the engine that answers searches (default golib's search engine) |
| `WithRank(r)` | the re-ranking stage, for the queries that ask for it (ADR 0215) |

### Run, the job queue and the writer

`Run(ctx)` first loads the jobs a previous run left, queues every document indexed under another
version than it would get now (`Revalidate`), and records the embedding model. Then it starts the
workers, the embedding loop (unless `WithExternalEmbedding`) and the reclaimer, and runs the writer
until `ctx` ends.

The writer is the one goroutine that writes the workspace's rows. In a loop it:

- turns touched paths into jobs, each with a new sequence number, so a result from an older read
  of the same path is discarded;
- hands queued jobs to the workers, which read, parse and chunk a file and write nothing;
- commits the results in batches (`WithBatching`), each batch one write transaction with the jobs,
  the change log and the documents;
- commits the vectors the embedding work hands it, and the other writes handed to it (reclaims,
  sweeps, `PurgeModel`);
- deletes dead chunks, orphaned vectors and old change-log rows when it has nothing else to do.

Jobs are rows too, so a restart takes up the work left. A file whose version and indexer identity
are unchanged is skipped without parsing, unless the job is forced. A file that cannot be read
waits before it runs again: 1 s, doubling to 5 min. A file over `MaxFileSize` leaves the index,
and its job waits for the file to change.

`Touch(path)` queues a path and never blocks; it and `Version` and `PathsUnder` make the `Indexer`
the queue and store `core/follow` follows into. `Reindex(ctx, path)` forces a path, or with `""`
every indexed document plus the files the `Rescanner` (`SetRescanner`) finds. A held document
(ADR 0216 §1.4: made by a chunker or a format this build lacks) is never reinterpreted:
`Reindex` of one alone is a `HeldError`.

### Kinds

Each file has a kind (`core/kind`: Markdown, text, YAML, registered, derived). The indexer's
behaviour per kind is held in `kind.Table`s, which panic at init when a kind lacks an entry:
`cutters` (how a kind is cut into chunks, links and frontmatter) and `identities` (what
`document.indexer` records for it). `Identity` and `ParseIdentity` read that string back.

### The writer port

`writer.go` is where documents are kept. A `Doc` is everything the indexer made of one file: its
version, its indexer identity, its metadata, chunks (`DocChunk`), links (`DocLink`), frontmatter
result and ADR number. A `Writer` begins a `Batch`; a `Batch` takes `Put` and `Delete` by path and
then `Commit` or `Rollback`: all of it, or none. Only document persistence crosses the port: the
job queue, the change log, readiness and publication stay the local store's.

The `Indexer` writes through the store's own implementation, a batch bound to its commit
transaction. `core/pgstore/pgwriter` is a second implementation, over PostgreSQL. Both pass the
conformance suite in `core/index/writertest`, which also checks that an implementation keeps every
field it does not declare deferred.

## Search

`Indexer.Search(ctx, q, QueryOpts)` runs a query through golib's search engine (or the one
`WithSearcher` gives), over a `View`: one read transaction, from which every retriever, signal and
snippet reads.

- **Lexical:** the FTS5 index `ChunkFTS` over the alive chunks, BM25 weighing title 10, breadcrumb
  5, tags 5 and body 1. Matched terms are marked with `HighlightStart` and `HighlightEnd`.
- **Semantic:** a 1-bit scan over the active model's published code snapshot, then the best
  candidates checked in the transaction and rescored by dot product.
- **Filters:** tags, paths and the schema's facets sit in the same WHERE as the match, so they apply
  before the rank and the limit. A facet the schema lacks is `ErrUnknownFacet`.
- **Stages** (`stages.go`): `QueryOpts.Stages` names the stages to run, `StageLexical`,
  `StageSemantic` and `StageRerank`, or `Mode` (`ModeAuto`, `ModeLexical`, `ModeSemantic`) does.
  `Result.Stages` reports the stages asked for, those run, and why each other one did not.
- **Rank:** with `WithRank`, a ranker re-orders the hits of a query that asks for it.
- **Retrieval:** a superseded document's hits can sit below its successor's, and a document's
  abstract can be a chunk of its own: the workspace's settings, which `QueryOpts.Retrieval`
  overrides per query.

`Result` carries the hits (`Hit`), the mode used, and the semantic and rank states.

## Embedding and model switching

With a provider, the indexer embeds the alive chunks that lack a vector, 64 distinct texts at a
time, and hands the vectors to the writer, which stores them and marks complete documents ready.
After each commit the writer publishes a new code snapshot for the queries. A text the provider
refuses is set aside for an hour while the rest go on. Without `WithExternalEmbedding`, Run's
embedding loop does this; with it, the daemon does, one batch per `EmbedBatch` call.

The provider's model is the target, fixed for an indexer's life. When no model is active, the
target becomes active. When another is, that one goes offline and the target fills: queries are
answered by words (`SemanticSwitching`; a semantic query is `ErrSwitching`) until the target
covers every alive chunk. The writer then makes it active in one transaction.

`Status` adds the embedding tier's state (`EmbeddingStatus`) and the held documents to the
store's; it is computed once per commit. `Models` lists the workspace's models and the room their
vectors take.

## Reclaim and orphan sweeps

A workspace keeps vectors for its active model and its target only (ADR 1791284787). Every other
model is reclaimed: its vectors in batches of 2000, each its own write transaction, then its row,
then the freed pages go back to the file system. A batch that finds the model active or targeted
again stops, and keeps what is left.

- A model the flip retired, or one no longer targeted, is queued on the indexer's reclaimer.
- `Sweep` reclaims every unused model, through the writer; `PurgeModel` one, at once, refusing the
  active model and the target (`ErrModelInUse`).
- `SweepOrphans` deletes the vectors whose text no chunk has any more.
- `SweepStore`, `ReclaimStore` and `SweepOrphansStore` do the same for a workspace no indexer runs.

## Tables

`tables.go` declares the index's tables through the store's seam (`store.Declare("index", …)`):
`document`, `chunk` (with its full-text index `chunk_fts`), `doc_tag`, `doc_alias`, `doc_name`,
`link_key`, `doc_facet`, `doc_diagnostic`, `link` (declared twice, joined to either end),
`embedding`, `index_job` and `change`. The migrations create them.

## Concurrency

- Call `Run` once per `Indexer`, and run one `Indexer` per workspace. `Run` starts the workers, the
  embedding loop (unless `WithExternalEmbedding`) and the reclaimer, runs the writer on its own
  goroutine, and returns once all of them have stopped.
- Only the writer writes the workspace's rows.
- Every other method of `Indexer` and `Store` is safe for concurrent use, whether `Run` runs or
  not. `Touch`, `Reindex` and `Revalidate` only queue paths, and the readers each use one read
  transaction.
- `Sweep`, `SweepOrphans`, `PurgeModel` and `EmbedBatch` hand their writes to `Run`'s writer and
  wait for it. With no `Run`, a call with something to write blocks until its ctx ends; `Sweep`
  and `SweepOrphans` also wait for `Run` to record its models.
- `SweepStore`, `ReclaimStore` and `SweepOrphansStore` write through the store directly, for a
  workspace no `Indexer` runs.
- What is given to `WithMatch`, `WithSchema`, `WithTextExtensions`, `WithProvider`,
  `WithSearcher` and `WithRank` is called from the workers and from searches at once, and must be
  safe for concurrent use.
