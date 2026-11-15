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
- Every time is the database's `clock_timestamp()`; no host's clock is compared with another's.

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
  version, 200 or 503.
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
  that, or `ann_defer_max` (24 h) after it first waited, whatever the backlog.
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
