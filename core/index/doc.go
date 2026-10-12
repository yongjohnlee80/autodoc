// Package index is a workspace's derived index in the store (core/store): documents, their chunks
// under per-document generations, full-text and semantic search over the chunks, tags, aliases,
// facets, links, and a change log. The files are canonical; everything here can be rebuilt from
// them. Every row is read and written through the workspace's store.Scope, on the tables this
// package declares to the store (tables.go): the package holds no SQL.
//
// A Store (NewStore) is one workspace's index, read: listings, the change log, the graph, status
// and a lexical search. An Indexer (New) keeps it current. Run's writer is the workspace's one
// writer: it turns the paths Touch and Reindex queue into jobs, hands them to parallel workers that
// read, parse and chunk the files, and commits their results in batches, through the writer port
// (Writer, Batch; conformance suite core/index/writertest). With an embedding provider the Indexer
// also fills vectors, switches models, reclaims the vectors of models no longer used, and answers
// semantic and hybrid queries. Each read runs in one read transaction on the WAL, so a reader sees
// every document at one generation, never half-written.
//
// Concurrency: call Run once per Indexer, and run one Indexer per workspace. Run starts the
// workers, the embedding loop (unless WithExternalEmbedding) and the reclaimer, runs the writer
// itself on the goroutine that called it, and returns once the writer's ctx ends and all of them
// have stopped. Only the writer writes the
// workspace's rows. Every other method of Indexer and Store is safe for concurrent use, whether Run
// runs or not: Touch, Reindex and Revalidate only queue paths, and the readers each use one read
// transaction. Sweep, SweepOrphans, PurgeModel and EmbedBatch hand their writes to Run's writer and
// wait for it: with no Run, a call with something to write blocks until its ctx ends (Sweep and
// SweepOrphans also wait for Run to record its models). SweepStore, ReclaimStore and
// SweepOrphansStore write through the store directly, for a workspace no Indexer runs. The
// functions and values given to WithMatch, WithSchema, WithTextExtensions, WithProvider,
// WithSearcher and WithRank are called from the workers and from searches at once, and must be safe
// for concurrent use.
package index
