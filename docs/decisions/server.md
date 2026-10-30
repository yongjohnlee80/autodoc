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

One writer per tenant is held by a PostgreSQL advisory lock, taken on a dedicated connection the
ingester keeps for its whole life, outside the pool. Losing that connection stops the ingester and
exits the server non-zero, so its supervisor restarts it; it never goes on writing unlocked. The
embedder writes a tenant only while its ingester confirms the lock.

In the code: `enterprise/ingest/`, `core/pgstore/writer.go`

## Incremental passes and full refreshes

A source is read two ways, both needing only `SELECT` on the business tables:

- **An incremental pass**, every interval, reads rows past the tenant's `(updated_at, id)` cursor,
  up to `now() − lag` (a minute by default), so a business transaction still open cannot commit a
  row behind the cursor. Each page and the new cursor commit in one transaction, so a crash replays
  at most one page.
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
