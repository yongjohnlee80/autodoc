# Server decisions

`autodoc --server` is AutoDoc's enterprise search server: a headless process that reads a business
database's rows through `.view` files, keeps them as documents, chunks and vectors in PostgreSQL,
and answers search for web applications. This page records the decisions that shape it and what
each one means in the code. How to run it is in [enterprise search](../enterprise/README.md).

## A headless server mode

`--server <server.toml>` is one more mode of the binary. It has no TUI, no local socket and no
SQLite store: it opens PostgreSQL, sets the configured schema as every pooled connection's
`search_path`, creates the schema if it is missing, and applies pgstore's migrations there. The
business tables are only read. The process is a stack of subsystems (the database, the schema, the
access policy, the ingesters, the embedder, the HTTP server), stopped in reverse.

The config is validated whole before anything runs; a refusal is exit code 2, naming the field.
Secrets (the DSN, the provider key, client tokens) are read from the environment or given as
hashes, never written in plain text. A sealed DSN is refused for now, since the keyslot lives beside
a local store the server does not have.

In the code: `app/server.go`, `enterprise/config/`

## A tenant per source, one writer each

Each `[[source]]` names one `.view` file and its own tenant. Sources refresh, re-cut and delete on
their own schedules, so sharing a tenant would let one view's refresh delete another's rows.

One writer per tenant is held by a lease: a row of `rag_writer_lease` in the server's schema,
claimed and renewed in transactions, never an advisory lock or any other session state, so a
dropped or recycled connection loses nothing.

- **Every write is fenced in its own transaction.** Its first statement confirms the lease (this
  holder, this epoch, not expired by the database's clock) and holds the row `FOR KEY SHARE` until
  the write ends. The embedding worker writes through the ingester's fenced store, so its writes
  are fenced the same way.
- **The holder's renew is never held up by its own writes.** A renew is a plain `UPDATE` of
  columns no unique index holds, which `FOR KEY SHARE` lets through, so a write longer than the
  lease keeps it.
- **A takeover waits for every open write.** A claim takes the row `FOR UPDATE NOWAIT`, which a
  fence refuses: no other server takes the tenant while a write of the holder's is open, and a
  claimer never queues on the row. An upsert claim would lock the row only as a renew does and
  take the lease over mid-write, so a claim is never one.
- **Only an expired or released lease is taken over,** at the next epoch. A stopping server
  releases its leases, so a standby takes them at its next poll; a crashed one's are taken a lease's
  length (30 seconds) after its last renew.
- **A holder that cannot renew stops by its own clock** before the database's lease runs out. A
  lease lost, taken over or not renewed in time, stops the ingester and exits the server non-zero,
  so its supervisor restarts it; it never goes on writing without the lease.
- **The leases have a pool of their own** (two connections), never the one searches use. On the
  shared pool, a renew waited behind a flood of searches past the lease's length, and the server
  stopped.
- **The index's pool is refused under two connections.** An index build pins a session for its
  DDL and renews its lease in a transaction beside it, so it holds two at once: on a pool of one,
  the first build waited for good and every search and write with it. A refusal at the start was
  chosen over a builder on one connection, which can't renew while its DDL runs.
- Every time is the database's `clock_timestamp()`; no host's clock is compared with another's.
- **A claim within milliseconds of a stop that cut a write can wait one poll.** The cut write holds
  the row `FOR KEY SHARE` until its backend sees its connection closed, so a claim landing then is
  refused as busy and comes back at the next poll, up to 5 seconds later. It's harmless, and has
  been since the lease came in: the lease was released, no second writer can start, and a
  supervisor's restart or a standby's poll comes later anyway.
- **v0.3.16 and v0.3.17 looked for a v0.3.15 server; v0.3.18 doesn't.** v0.3.15 held a tenant by
  a session advisory lock, which the lease doesn't see. It refuses a store migrated past its
  scripts, so only one running when the store was migrated, or its standby, can meet a newer
  server. Those two releases tried that lock's key before the lease was claimed, after it, and at
  every renew, and waited (or, holding the lease, stopped) while it was held. v0.3.18 upgrades only
  from v0.3.16 or later and has no such check: a v0.3.15 server still running beside it writes the
  tenant too, since the fence stops only servers that take the lease.

In the code: `core/pglease/`, `core/pgstore/writer.go`, `enterprise/ingest/`

## Incremental passes and full refreshes

A source is read two ways, both needing only `SELECT` on the business tables:

- **An incremental pass**, every interval, reads rows past the tenant's `(updated_at, id)` cursor,
  up to `now() − lag` (a minute by default), so a business transaction still open cannot commit a
  row behind the cursor. Each page and the new cursor commit in one transaction, so a crash replays
  at most one page. The lag must exceed the source's longest writing transaction. A pass is not read
  in one snapshot, and need not be: the row it could miss is one committed later than the lag after
  the `updated_at` it carries, which is commit order, not read consistency.
- **A full refresh**, on its schedule, renders every row and compares each document's version (a
  hash of the body, title, tags, facets and source id), so a change the view's `updated_at` missed is
  still caught. Paths the view no longer returns are deleted only after a complete, successful
  scan.
- **A row that fails** to render is quarantined with its error, not skipped, and retried on every
  pass until it renders or leaves the source.

A view must order by its cursor, and `export` may use only `id`; `--server --check` verifies the
order against the live database.

- **Deletes reach the index in the pass,** not only at the refresh. A row may say `deleted: true`
  (a soft delete, ordered with the rows by `updated_at`), or a source's deletes view returns a
  tombstone `{id, deleted_at}`, read past a cursor of its own in the same order and up to the
  same bound. Either deletes the document only when it is no older than the document's last write
  (`rag_document.source_at`), so the newest event wins; an equal time is a delete, since a
  deleted record that leaks is worse than a live one that waits for the refresh. A delete takes
  the vectors of the document's texts that no other document of the tenant shares. The refresh
  stays the backstop for every delete a source doesn't say.

In the code: `enterprise/ingest/ingest.go`, `core/pgstore/README.md`

## Embedding is central

One embedding worker serves every tenant, with the enterprise's model, through a shared limit of
requests in flight. Each distinct text is embedded once per tenant, where the data is read, so every
vector is in one vector space and users' machines embed nothing. Changing the model in the config is
a switch: the worker fills the new model while search answers by words, flips, then reclaims the
old model's vectors. A provider error backs off and never stops the server or ingestion.

In the code: `enterprise/embedder/embedder.go`

## Access is a separate predicate

Each source declares its access fields, and the server records them as the tenant's policy. Every
search carries a scope with a non-empty list of values for each access field; a tenant with no
policy, or a search with no scope, is refused. The scope is ANDed in SQL into every retrieval path,
apart from the query and the filter, and a query or filter that names an access field is refused.
It is never a search facet, because facets from the query text are OR-merged and could widen it.

In the code: `enterprise/search/search.go`, `core/pgstore/`

## Typed facets, filtered in SQL an index answers

A source declares the row fields a search may filter on, each typed: `keyword`, `number` or
`date`. The declaration is part of the tenant's policy, read in the snapshot that reads the access
fields. Facets never widen access: a name that is an access field (in any case), `tags`, `path`
or `title` is refused when the configuration loads, and a filter, a range or a query word naming
an access field is refused at search.

Each predicate is one an index answers, ANDed beside the access predicate and never merged with
it:

- **keyword:** `field=value` pairs in `rag_document.facets`, beside the access pairs. A field's
  values are one overlap, `facets && $n`, which the GIN index on `facets` answers; fields are
  ANDed. Matching is exact, case included.
- **number and date:** one row per value in `rag_document_value (tenant, doc_id, field, value)`,
  each a double (a date as Unix seconds). A range is `EXISTS` over that table with the bounds
  given, and its primary key `(tenant, field, value, doc_id)` answers it with a range scan. A
  list value is several rows, so a document matches when any of its values is within every bound.
  A side table and not a jsonb column, because a jsonb range needs an expression index per field,
  which means DDL per source on a table every tenant shares.

The values are replaced with their document, in its put's transaction, and go with it through
the foreign key. They are part of the document's content hash, so a changed value writes it again.

A complete refresh records the declaration it wrote its documents with (`rag_cursor.refresh_facets`).
While that differs from the source's declaration, a refresh is due at once and the status says
`refresh_due: "facets"`. Because the record is the store's, a change holds whichever server
loaded the new configuration: in a rolling restart, a server still on the old declaration may
complete a refresh after the change, and that refresh records the old declaration, so the next
ingester of the new one still finds the refresh due. The refresh embeds nothing again: embeddings
are keyed by the text, and the text is unchanged. A changed view is different: its documents are
cut again at the next scheduled refresh, not at once.

A request with ranges and no words is listed by the search package itself, since golib's engine
lists only what its own filter narrows. The approximate path's small-scope count reads the same
`where`, so a narrow range is answered exactly, without trying the index.

In the code: `core/pgstore/facets.go`, `enterprise/search/search.go`, `enterprise/ingest/source.go`

## GIN indexes without stale pending lists

A GIN index with `fastupdate` on (PostgreSQL's default) adds each write's entries to a pending list
that every scan reads whole until VACUUM merges it. After a bulk load, that list made every scoped
search slower: a label's facet scan read 3 ms of list for 37 entries.

- **The document's facets and tags** keep no pending list (`fastupdate = off`, script 000009,
  which also merges the lists already there). Their entries are a few short strings per
  document, so writing them straight into the index costs a load little.
- **The chunks' text index** keeps its list, since a chunk's words are many. The ingester merges
  it (`gin_clean_pending_list`) after a load: a refresh that wrote documents, or a pass that wrote
  many. Between loads autovacuum merges what trickles in.

In the code: `core/pgstore/gin.go`, `enterprise/ingest/ingest.go`

## One search across several tenants

A request may name several tenants, each with its own scope and filter, sharing the query, mode,
limit and grouping. Every tenant's grants and request are checked before any search runs, and one
refusal refuses all. The tenants are searched concurrently, each in its own snapshot under its own
access predicate (at most 4 at once per request, and `max_tenant_searches` across requests), and
the query is embedded once per model. The hits are merged by golib's fixed-scale relevance (the
RRF of each tenant's own retrievers) times an optional weight, every hit naming its tenant.

Fusing each arm across tenants instead (lexical by rank, semantic by distance) would need per-arm
scores out of the engine and helps only where tenants share a model. A server-side ranker would
rescore the merged hits anyway.

In the code: `enterprise/search/global.go`, `enterprise/api/api.go`

## A reranker after recall

A `[ranker]` section adds a second stage to every search: a cross-encoder (a TEI server, or a
Cohere-style rerank API) reorders recall's best candidates, the window (40 by default, 10 to 100).
A test on a 55,000-document catalog met its bar only with one: recall at 1 went from 0.47 to
0.76.

- **The texts are the search's own.** Each candidate's text is its chunk's breadcrumb and body,
  kept by the snapshot the search read it in, under its access predicate, as the rows are read.
  Nothing is read again, so a text can't come from another generation or outside the scope.
- **One call per search, after the merge.** A search across tenants reranks the merged top window
  once, each hit's text from its own tenant's snapshot. The ranker scores every candidate on one
  scale, which the merge by rank-based relevance lacks; a weight then decides which candidates
  enter the window. By default the window across tenants is at least 4 a tenant (at most 100), and
  a request may set its own.
- **A ranker never fails a search.** Unreachable, refused, rate limited, a bad answer, past its
  budget (`timeout`, 750 ms, the ranker's call alone), or no slot free in time: the search answers
  in recall order, and its `rank` says why. A ranker that is down is asked nothing while a prober
  brings it back, backing off to once a minute.
- **What leaves.** The ranker receives each reranked search's query and its window's texts; a
  hosted one receives them under its own terms. The log and the status carry codes and counts,
  never a query, a text or an answer.

Reranking each tenant and merging by the ranker's scores was rejected: a call per tenant, and a
cross-encoder's scores compare only within one call.

In the code: `enterprise/search/rank.go`, `enterprise/ranker/`, `core/pgstore/view.go`

## An HTTP API with a token per client

The same search package serves Go applications directly and `POST /v1/search` over HTTP/JSON.
Each `[[http.client]]` has its own bearer token, given as its SHA-256 or an environment variable,
and grants naming the tenants it may search and, per tenant, the access values it may ask for.

A request is checked in order: the token (compared in constant time, never logged), the body, the
tenant among the client's grants, the scope within those grants, then the search, which checks the
scope against the tenant's policy again. The API listens on loopback by default; elsewhere it needs
TLS, or an explicit `proxy = true` behind the operator's proxy.

In the code: `enterprise/api/api.go`, `enterprise/config/config.go`

## Operator health, status and metrics

- `GET /v1/health` is public and minimal: ok, degraded or unavailable, with no tenant names or
  version, 200 or 503. Its database ping runs on the leases' pool, never the searches': on theirs,
  a flood of searches made a busy server read unavailable, which a supervisor would restart.
- `GET /v1/status` answers only an operator's token (`[[http.operator]]`), for that operator's
  tenants: cursor and lag, passes and refreshes, quarantine, documents, models, lock and provider
  state. A search token is refused there, and one token in both roles is refused at load.
- Failures over HTTP are a closed set of codes; the text stays in the log. Metrics are logfmt lines
  in the log at a set interval, with no secrets, query text or client names.
- `autodoc --server --health` probes the server's own address, and `deploy/` has a systemd unit and
  a container example.

In the code: `enterprise/ops/ops.go`, `enterprise/opcode/`, `app/server_health.go`

## A model's index waits for its bulk load

Each embedding model has one HNSW index, shared by every tenant. Inserting into a model with its
index built costs several times what inserting into one without does, so a tenant's first import
would pay that cost on every embedding if the index were built as soon as the tenant named the
model.

- A model's index waits, whenever it is wanted, while a tenant whose active model it is has
  `ann_defer_backlog` (2,000) or more documents not yet ready: its first build, and a rebuild after
  its index went missing. It is built, concurrently, once every such tenant's backlog falls below
  that, or `ann_defer_max` (24 h) after it first waited, whatever the backlog. That deadline is
  kept until the index leaves wanted, so a tick that can't claim it (the builder busy with another
  build) never starts a new one, and a backlog that drains and fills again waits only for the rest
  of it.
- A backlog that can't be counted holds every wanted model for that tick, and the builder reports
  the error: a load never pays the index's cost because a count failed.
- The backlog is counted only while a model's index is wanted, capped at `ann_defer_backlog`: at a
  full backlog the count stops after that many rows, and once drained it reads the partial index
  of unready documents, so it stays cheap whether a load is under way or done.
- Semantic search is exact while the index waits, as it is while any index is wanted. The status
  says `ann-building` with the reason and the time it will be built by.
- Measured at 1,000,000 documents, the import reaches a ready index in 14 min 39 s, the build
  included, against 1 h 21 min with the index built first; recall is the same
  ([operations](../enterprise/operations.md#the-approximate-searchs-index-what-it-costs)).
- The session that builds an index sets its own `maintenance_work_mem`
  (`ann_maintenance_work_mem`, 512 MB). A parallel build keeps that memory in the database's
  `/dev/shm`. When that runs out (SQLSTATE 53100), the half-built index is dropped and the build
  runs again without parallel workers, and the server logs a warning that the database's
  container needs a larger `--shm-size`.

Known limits:

- A model whose index is built already doesn't wait: a tenant importing into it pays the index's
  cost: 206 embeddings a second at 1,000,000, against 1,138 for a deferred import, its build
  included. Dropping and rebuilding a shared index for one tenant's load would slow every other
  tenant's searches.
- A model that is only a tenant's target (the fill of a model switch) doesn't hold the build back:
  the backlog counted is the active model's.
- A failed build's retry isn't held: it is due on its own backoff, so a retry during a load pays
  the per-insert cost.

In the code: `core/pgstore/annbulk.go`, `core/pgstore/annbuild.go`
