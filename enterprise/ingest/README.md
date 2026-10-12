# enterprise/ingest

Indexes an enterprise's database rows into pgstore through `.view` files: each row a source view
returns is rendered to a document, chunked, and written into the source's tenant, and kept current
as the rows change.

```go
import "github.com/yongjohnlee80/autodoc/enterprise/ingest"

src, err := ingest.Load("views/track.view", []string{"label_id"})
rep, err := src.CheckOrder(ctx, business, time.Now()) // against the live source, read-only

st, _ := pgstore.New(store, "lm-catalog")             // store: a pool opened WithSchema
in := ingest.New(st, "lm-catalog", src, business, locks, ingest.Options{Schema: "autodoc_search"})
err = in.Run(ctx) // until ctx ends; ErrLockLost if the tenant's lock is lost
```

## API

- **A source:** `Load(path, access)` reads a `.view` file and refuses (`*ViewError`, naming the
  view's field) one an ingester can't page: `Args` must be `[after_ts, after_id, upto_ts]`, the
  query must use `$1` to `$3`, and `export` may print `{{.id}}` and nothing else of the row.
  `Source.Name`, `File`, `Access`, `Indexer` (the identity its documents are cut under) and `Path`
  (a row's document path, from its id).
- **The order:** `Source.CheckOrder(ctx, conn, upto)` reads the view in one read-only snapshot and
  resumes it at its page boundaries and between rows sharing a timestamp, refusing a view whose
  order isn't its cursor's. `OrderReport` says what it confirmed and could not exercise
  (`CheckRows`, `CheckProbes`).
- **The ingester:** `New(store, tenant, source, business, locks, Options)`.
  - `Acquire` and `Release` hold the tenant's writer lock, on a pinned connection of `locks`.
  - `Pass` reads the rows past the cursor; `Refresh` reads every row, then deletes the documents of
    rows gone; `Run` does both on schedule. Each returns `Stats`.
  - `Options`: `Schema` (required), `Refresh`, `Interval`, `Lag`, `PingEvery`, `LockPoll`, `Log`,
    `Now` (the source's clock) and `Clock` (the time a refresh records).
- **Errors:** `ErrLockLost`, `ErrNotLocked`, `ErrOrder`, `ErrStalled`; `RetryLimit`; the fields
  every row carries, `FieldID`, `FieldUpdatedAt`, `FieldTitle`, `FieldTags`; `CutVersion`.

## What it guarantees

- **One writer per tenant.** The lock is a session advisory lock on a connection pinned for the
  ingester's life, outside the pool, confirmed before every commit and every `PingEvery`. A lost
  lock writes nothing more and stops `Run` with `ErrLockLost`.
- **A crash replays at most one page.** Each page's documents, quarantined rows and cursor are one
  transaction.
- **No row is skipped.** The cursor is `(updated_at, id)`, so rows sharing a timestamp across a page
  boundary are read; a pass reads up to `now() − Lag`, behind any business transaction shorter than
  the lag; a row that fails is quarantined and retried on every pass.
- **No document is deleted on a partial read.** A refresh deletes only after a scan that read every
  row, and a row whose body fails still counts as seen.

Writing a source view: [docs/enterprise/views.md](../../docs/enterprise/views.md).

## Concurrency

A `Source` is immutable and safe for concurrent use. An `Ingester` belongs to one goroutine: call
`Acquire`, `Pass`, `Refresh`, `Run` and `Release` from it alone. `Run` starts one goroutine of its
own, which only checks the lock, and waits for it before returning.
