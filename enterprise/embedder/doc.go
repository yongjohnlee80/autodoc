// Package embedder is the enterprise search server's one embedding worker: it embeds every
// tenant's chunk texts with the enterprise's model, centrally, once per distinct text, and stores
// the vectors in pgstore, so the server's tenants can be searched by meaning as well as by words.
//
// # What it does
//
// Run sets up the provider (Build: embed.NewOpenAI or embed.NewOllama, which probe the model for
// its size), then works each tenant in turn:
//
//   - the texts pgstore lists as Pending under the model, the scan resumed where the last round
//     stopped (and, outside a switch, reading the unready documents alone), are embedded in
//     batches, through a limited number of slots shared by every tenant, with embed.Bisect: each
//     vector normalized, and a text the provider rejects set aside for a while, never failing
//     its batch;
//   - each batch's vectors are stored (pgstore's Embed, keyed by the text's hash, so a text two
//     chunks share is embedded once), and the documents they complete are marked ready;
//   - a document written again with texts already embedded is marked ready when nothing is
//     pending.
//
// The model's identity is its fingerprint (provider, name, digest and dimensions). A tenant whose
// active model is another is switched: the new model is recorded as the target and filled while
// searches answer by words, every document is marked by it, it becomes active, and the old
// model's vectors are reclaimed. A switch interrupted by a restart carries on; one whose target
// is no longer configured is cancelled.
//
// A failure of the provider or of the database is logged, and the worker waits before it tries
// again, the wait doubling to a limit. Nothing stops it, and ingestion never waits on it.
//
// # One writer per tenant
//
// The worker writes a tenant only while Tenant.Confirm answers nil, which the server wires to the
// tenant's ingester (ingest.Ingester.Confirm): the tenant's writer lock is this process's. It
// confirms before each of its writes, so a second server waiting for a tenant never touches it.
//
//	w := embedder.New(build, []embedder.Tenant{{Name: "lm-catalog", Store: st, Confirm: in.Confirm}},
//		embedder.WithSlots(4), embedder.WithLogger(log))
//	go w.Run(ctx) // until ctx ends
//	w.Wake()      // after an ingester commits
//
// Concurrency: Wake may be called from any goroutine. Run is called once, and runs a tenant's
// batches on goroutines of its own, at most the slots at a time, waiting for them before it
// returns. A Store is used concurrently by those batches, as a *pgstore.Store may be.
package embedder
