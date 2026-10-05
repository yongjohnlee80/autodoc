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
- **Lexical:** a generated `simple` tsvector (breadcrumb A, body D), `dao.Match` with
  `query.TSQuery`, ranked by `dao.RankQuery`, then path, then ordinal; snippets cut in Go with
  `chunk.Highlight`.
- **Semantic:** an exact scan ordered by `dao.Distance(…, dao.Cosine)` over documents ready under
  the active model; `search.ErrModelChanged` for another model; `StateSwitching` while a target
  model fills, `StatePartial` while a document is not ready.
- **Filters** in the same WHERE, before rank and limit: tags ANDed (`ArrayOp`), paths ORed and
  literal (`dao.HasPrefix`, so `%`, `_`, `\` and `!` match themselves), facet values ORed within a
  field and ANDed across fields. Paths compare byte by byte.
- **Writer:** `Put` replaces a document's chunks and links in one transaction and bumps its
  generation; `Embed` stores vectors; `MarkReady` marks a document ready when every chunk has a
  vector under the model; `Delete`; `SetModels`.
- **Migrations** through `dao/deploy`, in the ledger `autodoc_schema`.

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

## Testing

The cells run against a PostgreSQL with pgvector whose user may create databases, named by
`AUTODOC_TEST_PGURL` (VM43's `autodb-r3-pg`); each creates and drops a scratch database. Without
it they skip.

## License

Apache-2.0: see [LICENSE](../../LICENSE).