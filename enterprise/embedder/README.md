# enterprise/embedder

The enterprise search server's one embedding worker: it embeds every tenant's chunk texts with the
enterprise's model, centrally, once per distinct text, and stores the vectors in pgstore.

```go
import "github.com/yongjohnlee80/autodoc/enterprise/embedder"

build := func(ctx context.Context) (embed.Provider, error) {
	return embed.NewOpenAI(ctx, "https://embeddings.example.internal", key, "text-embedding-3-small", client)
}
w := embedder.New(build, []embedder.Tenant{{Name: "lm-catalog", Store: st, Confirm: in.Confirm}},
	embedder.WithSlots(4), embedder.WithLogger(log))
go w.Run(ctx) // until ctx ends
w.Wake()      // after an ingester commits (ingest.Options.Committed)
```

## API

- `New(build, tenants, opts...)`: a worker over `tenants`, each a `Tenant{Name, Store, Confirm}`.
  `Store` is the port the worker uses, a `*pgstore.Store`; `Confirm` answers nil while the tenant
  is this process's to write (its ingester holds the writer lock).
- `Run(ctx)` sets up the provider, retrying until it answers, then embeds until `ctx` ends.
- `Wake()` asks it to look at its tenants now.
- Options: `WithBatch` (texts per call, 64), `WithSlots` (calls in flight across every tenant, 4),
  `WithRetry` (the first and longest wait after a failure, 1s and 1m), `WithPoll` (how often the
  tenants are looked at with no wake, 30s), `WithRefusedFor` (how long a rejected text is set
  aside, 1h), `WithLogger`, `WithClock`.

## What it guarantees

- **Once per distinct text.** Pending lists each text hash once, and vectors are keyed by it.
- **Each chunk read about once.** A tenant's rounds resume Pending where the last one stopped, and
  PostgreSQL decides which texts lack a vector; outside a model switch only unready documents are
  read, so a ready tenant's new document costs its own rows. The scan looks again from the start
  only when it reaches the end from part way, when a batch failed, and when a set-aside text's
  time is up.
- **Bounded.** At most the slots' calls are in flight, whichever tenants they are for; each call
  embeds at most a batch, and is bounded in time.
- **Retries, never blocks.** A provider that fails (down, rate limited, refusing the key) is asked
  again after a wait that doubles to a minute; a text it rejects is set aside, and the rest of its
  batch is stored. Ingestion never waits on the worker: until a document's vectors are in, it
  answers by words.
- **One writer.** It writes a tenant only after `Confirm`, before every write.
- **A model change keeps search answering.** The new model is filled as the target while the old
  one stays recorded as active and searches answer by words (`semantic: "switching"`); every
  document is marked by the new model before it becomes active; then the old vectors are
  reclaimed. A restart in the middle carries on.

## Concurrency

`Wake` may be called from any goroutine; `Run` once. A round's batches run on goroutines of their
own, at most the slots at a time, and use the `Store` concurrently.
