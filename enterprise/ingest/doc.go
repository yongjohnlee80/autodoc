// Package ingest indexes an enterprise's database rows into pgstore through .view files: each row
// of a source view is rendered to a document, chunked, and written into the source's tenant, so a
// web application can search the enterprise's data (enterprise/search) while the rows change.
//
// # A source view
//
// A source is a golib .view file whose query takes the cursor's arguments, [after_ts, after_id,
// upto_ts], and returns, in (updated_at, id) order, the rows after the cursor up to upto_ts, each
// as one JSON object carrying its id, its updated_at and the tenant's access fields. Its export
// names a document by the row's id alone. Load checks what can be checked in the file; CheckOrder
// checks the order against the live source.
//
//	src, err := ingest.Load("views/track.view", []string{"label_id"})
//	rep, err := src.CheckOrder(ctx, business, time.Now())
//
// # Ingesting
//
// An Ingester is the only writer of its tenant: it holds the tenant's advisory lock on a pinned
// connection of its own, and confirms it before every commit. Pass reads the rows past the
// tenant's cursor, page by page, each page one pgstore.Batch with its documents, its quarantined
// rows and the cursor; Refresh reads every row, writes the documents whose content changed, and
// deletes the documents of rows gone, only after a scan that read every row. A row that fails
// (a missing field, a render error) is quarantined and retried on every pass, never skipped.
//
//	in := ingest.New(store, "lm-catalog", src, business, locks, ingest.Options{Schema: "autodoc_search"})
//	err := in.Run(ctx) // until ctx ends; ErrLockLost if the lock is lost
//
// A caller with its own schedule calls Acquire, then Pass and Refresh, then Release.
//
// Another writer of the tenant in the same process, the embedding worker, writes only while
// Confirm says the ingester holds the lock, and Options.Committed wakes it after each commit.
//
// Concurrency: a Source is immutable and safe for concurrent use. An Ingester belongs to one
// goroutine: Acquire, Pass, Refresh, Run and Release are called from it alone; Confirm may be
// called from any goroutine. Run starts one goroutine of its own, which only checks the lock, and
// waits for it before returning.
package ingest
