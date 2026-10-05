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
  removes the tenant whole; `SetModels` records the active and target models.
- **Migrations** through `dao/deploy`, in the ledger `autodoc_schema`.

## Access control is yours, not pgstore's

pgstore enforces two things.
- **The tenant boundary:** every statement is filtered by the tenant the Store was opened with.
- **Filters applied before rank and limit:** tags are ANDed, paths ORed, and facet values ORed
  within a field and ANDed across fields. So a filtered query never loses an admitted hit to
  the limit.

pgstore knows nothing of users or groups. Mapping a user to the tenants and the facet values
they may see is your application's business logic. Do it server-side: open the Store with a
tenant your authorization chose, never one taken from a request, and add the access facets to
every query's filter yourself.

## Example

```go
conn, err := postgres.Open(ctx, dsn)
if err := pgstore.Migrate(ctx, conn); err != nil { … }
s, err := pgstore.Open(conn, "tenant-42")
id, err := s.Put(ctx, pgstore.Document{Path: "manuals/pump.md", Tags: []string{"pump"}, Chunks: chunks})
err = s.Embed(ctx, model, vecs)
ready, err := s.MarkReady(ctx, "manuals/pump.md", model)
engine := search.NewEngine[int64, *pgstore.View](s, …)
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