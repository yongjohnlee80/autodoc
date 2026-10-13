# pgstore

A tenant-scoped PostgreSQL + pgvector `search.Store`, built on golib's `dao` alone.

## Install

```go
import "github.com/yongjohnlee80/autodoc/core/pgstore"
```

PostgreSQL with the `vector` extension available (the migration creates it).

## Features

- **golib's search port:** `search.Store[int64, *pgstore.View]`; the view is `Semantic`,
  `Signaler` and `Lister`, and passes golib's `searchtest.Run` conformance suite.
- **Tenancy:** every key starts with the tenant, every join and upsert carries it, every statement
  is filtered by it. Embeddings are keyed `(tenant, model, text_hash)`.
- **One snapshot per View:** a REPEATABLE READ, READ ONLY transaction.
- **Lexical:** a generated tsvector over title (class A), breadcrumb (B), tags (C) and body (D) —
  the local store's 10 : 5 : 5 : 1 weights column for column — stemmed by a `rag_english`
  configuration the migration creates (Snowball English, no stop-word removal, so every query
  word stays required); `dao.Match` with `query.TSQuery`, ranked by `dao.RankQuery`, then path,
  then ordinal; snippets cut in Go with `chunk.Highlight`.
- **Semantic:** an exact scan ordered by `dao.Distance(…, dao.Cosine)` over documents ready under
  the active model; `search.ErrModelChanged` for another model; `StateSwitching` while a target
  model fills, `StatePartial` while a document is not ready.
- **Filters** in the same WHERE, before rank and limit: tags ANDed (`ArrayOp`), paths ORed and
  literal (`dao.HasPrefix`, so `%`, `_`, `\` and `!` match themselves), facet values ORed within a
  field and ANDed across fields. Paths compare byte by byte.
- **Writer:** `Put` replaces a document's chunks and links in one transaction and bumps its
  generation, storing the source `Version` and the `Indexer` identity verbatim and each chunk's
  `Embed` text; `Embed` stores vectors; `MarkReady` marks a document ready when every chunk has a
  vector under the model; `Pending` lists the unembedded texts for a worker; `Docs` pages a
  tenant's documents with their version and indexer; `Delete` removes one document; `Drop`
  removes the tenant whole; `SetModels` records the active and target models, and `Models` reads
  them back.
- **For an embedding worker:** `Pending(ctx, PendingQuery{Model, After, Unready, Skip, Limit})`
  lists the texts with no vector under a model, each once, after a `PendingKey` (a chunk's
  `(doc, ord)`) and returns the key to resume from (zero at the end), so a worker paging through a
  tenant reads each chunk about once. The vector's absence is an index probe of `rag_embedding`'s
  key per chunk read, decided in PostgreSQL; `Unready` reads only the chunks of unready documents,
  through a partial index, which is right for the active model;
  `DocsWithTexts` the documents holding given texts, looked up by hash, to ask `MarkReady` about
  after their vectors are stored; `Unready` pages the documents not ready, for the ones whose texts
  were all embedded already (a document written again unchanged, or a crash between `Embed` and
  `MarkReady`).
- **Chunk kinds:** each chunk is a `section` or an `abstract` (`Document.Kinds`, by position);
  `WithSectionsOnly(ctx)` leaves the abstracts out of Lexical and Semantic, before the limit.
- **Ingest state** for a server that indexes a source into a tenant (ADR 1791762085), written
  through a `Batch` beside the documents it covers (a pass's page, or the batch that records a
  refresh):
  - `SetCursor(ctx, Cursor{UpdatedAt, ID})`: the `(updated_at, id)` the last committed page passed,
    kept to the microsecond and rounded down, so a crash replays at most one page and never skips a
    row; `SetRefreshed` records the last full refresh beside it. `Store.Cursor` and
    `Store.Refreshed` read them back.
  - `Quarantine(ctx, sourceID, err)` and `Release(ctx, sourceID)`: a source row that failed, kept
    with its error and when it first and last failed. `Store.Quarantined(ctx, limit)` pages the row
    retried longest ago first.
- **Access policy:** `SetPolicy` / `Policy` keep a tenant's access fields and view name; no policy
  is `ErrNoPolicy`, and one with no field is refused (`ErrNoAccess`).
- **Migrations** through `dao/deploy`, in the ledger `autodoc_schema`: `000001` is the baseline,
  `000002` adds the chunk kind and the ingest tables (`rag_cursor`, `rag_ingest_error`,
  `rag_tenant_policy`), `000003` the partial indexes of unready documents, each with its revert.
- **One writer per tenant:** `AcquireWriter(ctx, locks, schema, tenant, poll, waiting)` takes the
  tenant's advisory lock (`WriterKey`) on a connection it pins from `locks` for as long as it is
  held, waiting while another session holds it; `Check` confirms it is still this session's (else
  `ErrWriterLost`), `Release` lets it go, `Backend` is its connection's process id. A lock taken
  through a pool would stay on a connection the pool recycles while another writer takes it.
- **The database's clock:** `Now(ctx, conn)`, the clock a source's `updated_at` values are written
  with.
- **A schema of its own:** `WithSchema(schema)` sets `search_path` on every pooled connection, and
  `EnsureSchema` creates the schema (see below).

## Access control

pgstore enforces three things.
- **The tenant boundary:** every statement is filtered by the tenant the Store was opened with.
- **Filters applied before rank and limit:** tags are ANDed, paths ORed, and facet values ORed
  within a field and ANDed across fields. So a filtered query never loses an admitted hit to
  the limit.
- **An access scope, when you read through `ViewWithin`:**
  `Store.ViewWithin(ctx, access map[string][]string, fn)` gives `fn` a View whose every retrieval
  (Lexical, Semantic, List and Signals, and SemanticState's count) ANDs a predicate over the
  document's facets, in SQL, **apart from the filter**. Within a field the values are the allowed
  ones (ORed); across fields every one must hold (ANDed); a document with no value for a field is
  outside. A filter, or a query whose text names an access field, can only narrow what the scope
  admits: golib's engine OR-merges a query's facets into the filter, which is why a scope must
  never be one of the filter's facets. Signals counts in-links only from documents the scope
  admits. An empty scope, a field with no values or a field name with `=` is `ErrNoAccess`:
  there is no scope that reads everything. `View` stays unscoped.

pgstore knows nothing of users or groups. Mapping a user to the tenants and the scope values they
may see is your application's business logic. Do it server-side: open the Store with a tenant your
authorization chose, never one taken from a request, and read through `ViewWithin` with the scope
your authorization computed. Checking that a scope names every field of the tenant's `Policy` is
the caller's (AutoDoc's `enterprise/search` does it).

## A schema of its own

pgstore's tables, its `rag_english` configuration and its ledger are created and found through the
connection's `search_path`, never by a qualified name. golib's `postgres.Open` is a pool, so a
`search_path` set once, on one connection, leaves the others in `public`. Set it on every physical
connection, and create the schema before migrating:

```go
conn, err := postgres.Open(ctx, dsn, pgstore.WithSchema("autodoc_search"))
err = pgstore.EnsureSchema(ctx, conn, "autodoc_search") // a plain identifier, or ErrSchemaName
err = pgstore.Migrate(ctx, conn)
```

- `WithSchema` runs `SET search_path TO "schema", public` on each connection the pool opens,
  replacements included, before it serves; `public` stays for a `vector` extension installed
  there.
- `EnsureSchema` must come before `Migrate`: PostgreSQL creates an object in the first schema of
  the path that **exists**, so with the schema missing the tables would land in `public`.
- `EnsureSchema` creates a missing schema, which needs `CREATE` on the database, and leaves one that
  exists alone, with no statement that needs it. So a DBA can create the schema for the role
  (`CREATE SCHEMA autodoc_search AUTHORIZATION role`) and install `vector` in `public`, and the
  role then needs nothing on the database itself.

## Concurrency

A `Store` is safe for concurrent use, and so are its readers (`View`, `ViewWithin`, `Docs`,
`Cursor`, `Quarantined`, `Policy`). A `View` is valid only inside its `fn`, and one `View` belongs
to one goroutine. A `Batch` is one transaction and belongs to one goroutine: its documents, cursor
and quarantine commit together or not at all. Writes conflict per document: a `Put` that loses its
compare-and-set to another writer is retried alone and fails a `Batch` with `ErrWriteRace`. A
`Store` does not make a tenant single-writer by itself: a server that moves a tenant's cursor holds
the tenant's writer lock (`AcquireWriter`), a session advisory lock on a connection pinned from a
pool of its own, and confirms it (`WriterLock.Check`, `ErrWriterLost`) before each commit. A
`WriterLock`'s methods are safe for concurrent use. `WithSchema`'s hook runs on every connection
the pool opens, so concurrent queries all run in the schema.

## Example

```go
conn, err := postgres.Open(ctx, dsn)
if err := pgstore.Migrate(ctx, conn); err != nil { … }
s, err := pgstore.New(conn, "tenant-42")
id, err := s.Put(ctx, pgstore.Document{Path: "manuals/pump.md", Tags: []string{"pump"}, Chunks: chunks})
err = s.Embed(ctx, model, vecs)
ready, err := s.MarkReady(ctx, "manuals/pump.md", model)
engine := search.NewEngine[int64, *pgstore.View](s, …)

// a page of a source, with the cursor it moved, in one transaction
b := s.Begin(ctx)
_, err = b.Put(ctx, doc)
err = b.Quarantine(ctx, "row-17", renderErr)
err = b.SetCursor(ctx, pgstore.Cursor{UpdatedAt: last.UpdatedAt, ID: last.ID})
err = b.Commit()

// a caller's read, within the scope your authorization computed
err = s.ViewWithin(ctx, map[string][]string{"label_id": {"A"}}, func(v *pgstore.View) error { … })
```

## A destination that holds the old scripts

Before this package, a destination's tables were a script set this repository has deleted. A
database whose `autodoc_schema` ledger records those scripts is **refused** by `Migrate` with
`dao/deploy`'s `ErrDowngrade`, before anything runs: nothing is dropped, nothing is reset, and
the database is left exactly as it was.

Recovery is an operator's deliberate, by-hand choice. Prefer pointing the destination at a
**fresh schema**. The old table names (`workspace`, `document`, `chunk`, `doc_tag`, `doc_alias`,
`doc_name`, `doc_facet`, `doc_diagnostic`, `link`, `model`, `embedding`) are unprefixed and may
belong to another product in a shared schema, so never drop them unread: first verify the
`autodoc_schema` ledger in that schema names them, and that their owner agrees. Only then drop
the old destination tables and the ledger rows in that schema — the `vector` extension stays —
and let `pgstore.Migrate` apply the new baseline. Nothing is lost that cannot be rebuilt: a
destination is derived from the workspace's files, and re-indexing into a new destination
reproduces it.

## Testing

The cells run against a PostgreSQL with pgvector whose user may create databases, named by
`AUTODOC_TEST_PGURL`; each creates and drops a scratch database. Without it they skip, unless
`AUTODOC_TEST_PG_REQUIRED=1` says the run has a service it must not skip on, in which case a
missing URL fails loudly.

## License

Apache-2.0: see [LICENSE](../../LICENSE).