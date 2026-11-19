# Running the server

How to run `autodoc --server` in production and watch it. It covers:
- its health endpoint, for load balancers and supervisors;
- its status endpoint, for operators;
- the metrics lines in its log;
- a systemd unit and a container image, both in the repository under [`deploy/`](../../deploy).

## Health: `GET /v1/health`

Public: it needs no token, and it names no tenant, model, count or version.

```sh
curl -s http://127.0.0.1:8730/v1/health
```

```json
{"status": "ok", "checks": {"database": "ok", "tenants": "ok", "embedding": "ok"}}
```

| Check | ok | degraded | failing |
|---|---|---|---|
| `database` | the index's database answers a ping within 2s, on a connection searches don't use, so a server busy with searches still reads ok (the source's database is checked by the passes: see `/v1/status`) | | it doesn't |
| `tenants` | every tenant's writer lease is held | a lease is held by another server (this one is a standby) | |
| `embedding` | the provider is up | it's down or failing: searches answer by words meanwhile | |

- **`status`:**
  - `ok`;
  - `degraded`: searches still answer, by words or for the other tenants;
  - `unavailable`: the database fails, or the server is stopping.
- **HTTP status:** 200 for ok and degraded, 503 (with `Retry-After`) for unavailable. Keep a degraded server in rotation: it still answers.
- **The states are read at each request; the database's ping is kept for a second.** Anyone may ask for health, so a caller asking in a loop costs the database at most one ping a second. The tenants' leases and the provider's state are read as they are now.

## Status: `GET /v1/status`

Per tenant, for **operators**. Status shows tenant-wide figures, which a search client's grants, given per access value, don't cover. So an operator has a token of its own:

```toml
[[http.operator]]
name      = "ops"
token_env = "AUTODOC_OPS_TOKEN"   # or token_sha256, as clients
tenants   = ["music"]        # or ["*"]
```

- **An operator's token reads status for its tenants, and can't search** (401 on `/v1/search`).
- **A search client's token on status is 403.**
- **One token can't hold both roles;** the configuration refuses it.

```sh
curl -s http://127.0.0.1:8730/v1/status -H "Authorization: Bearer $AUTODOC_OPS_TOKEN"
```

```json
{"as_of": "2026-10-13T09:41:07Z",
 "server": {"version": "v0.1.40", "started": "2026-10-13T09:00:00Z",
            "embedding": {"model": "openai|text-embedding-3-small||1536", "state": "up", "error": "",
                          "batch": 64, "in_flight": 4, "last_call_ms": 812},
            "ranker": {"kind": "tei", "model": "BAAI/bge-reranker-v2-m3", "state": "up", "window": 40,
                       "ranked": 5120, "unranked": {"timeout": 3}, "skipped": 210}},
 "tenants": [{"tenant": "music", "lock": "held",
   "cursor": {"updated_at": "2026-10-13T09:41:03Z", "id": "8812", "lag_seconds": 4},
   "last_pass": {"at": "…", "read": 12, "written": 12, "quarantined": 0, "error": ""},
   "last_refresh": {"at": "…", "complete": true, "last_complete": "…", "error": ""},
   "quarantine": 0,
   "documents": {"total": 120344, "ready": 120344},
   "models": {"active": "openai|…", "target": "", "semantic": "ready", "search": "ann"},
   "error": ""}],
 "ann": [{"model": "openai|text-embedding-3-small||1536", "mode": "ann", "underfill": 3, "exact_scoped": 41}]}
```

| Field | What it tells you |
|---|---|
| `server.embedding` | the provider's `state` (`starting`, `up`, `down`) and its `error` code; the texts one request carries now (`batch`) and the requests in flight (`in_flight`), which are `[embedding] batch` and `slots` unless requests ran out of `call_timeout` and the pace stepped down ([server-config](server-config.md#how-the-server-embeds)); the last request's time (`last_call_ms`), and how many requests ran out of their time since the server started (`timeouts`, absent while none). A `batch` below yours that stays there means the provider is too slow for it. |
| `server.ranker` | with a `[ranker]` only: its `kind` and `model`, its `state` (`starting`, `up`, `down`) and a down state's `error` code, the configured `window`, and since the server started the searches it reranked (`ranked`), answered in recall order by why (`unranked`: the codes of a search's `rank.error`), and skipped for `"rerank": false` (`skipped`). A `timeout` or `busy` count that grows means the ranker is too slow for `timeout` or `slots` ([server-config](server-config.md#how-the-server-reranks)). |
| `lock` | `held`; `waiting` (another server holds the tenant: a standby); `none` |
| `lock_wait` | why a `waiting` lock waits when it isn't for another server of this release: `older_server`, a server of v0.3.15 or before holding the tenant by its advisory lock ([upgrading from v0.3.15](#upgrading-from-v0315)). Stop that server. Absent otherwise. |
| `cursor.lag_seconds` | how far the index is behind your data, on your (the source) database's clock: now, less the bound the last pass that completed read to (the database's now less `lag` when it ran). On a quiet source it stays at most `lag` plus `interval`; it grows only while passes fail or can't keep up. Alert on it. It's absent on a standby (only the server holding the tenant's lease measures it) and before that server's first complete pass, so make absence an alarm of its own: absent on every server of a tenant for longer than an `interval` means no server is completing passes. `cursor.updated_at` and `id` are the last row read, which stays put while nothing changes. |
| `last_pass`, `last_refresh` | when each last ran, what it read and wrote, and its error code. `complete` is false for a refresh that couldn't read every row (it deletes nothing then; see [views](views.md)). |
| `deletes` | a source with a deletes view: its cursor (`updated_at` is the last tombstone's `deleted_at`), its `lag_seconds` measured as the rows' is (from the bound its last deletes pass that succeeded read to), and its last pass's `error` (`view`, `order`, or none). A deletes view that fails holds only the deletes; its lag grows while it does. |
| `refresh_due` | why a refresh runs at once rather than on its schedule, until it completes: `facets` while its last refresh wrote another facet declaration than the source's. In a mixed fleet, one server on the old declaration and one on the new, each refreshes the tenant when it takes the lease, until every server has the same configuration: right, but a cost to roll out quickly ([server-config](server-config.md#sourcefacets-what-a-search-may-filter-on-optional)). Absent otherwise. |
| `vectors_swept` | how many orphan vectors (of texts no document has any more, under any model) the tenant's ingester has deleted since the server started. It sweeps after every complete refresh, and after a pass that wrote at most once every 15 minutes, so the store no longer only grows as documents change. Absent while none. |
| `quarantine` | rows that failed to render and wait to be fixed in your data (the log names them) |
| `documents` | the tenant's documents, and how many can be found by meaning |
| `models.semantic` | `ready`, `partial` (embedding), `switching` (a model change filling; searches answer by words), or `off` |
| `models.search`, `models.search_reason` | how the tenant's searches by meaning run: `exact` (below `ann_threshold` embeddings, or a model with no index; the reason says which), `ann` (approximate, through the model's HNSW index), `ann-building` (the index is being built; exact meanwhile), or `ann-failed` (exact; the reason is the build's error) |
| `ann` | each embedding model's index: its `mode`, the `reason` when it failed, and `underfill`, how many approximate searches came back short (a selective filter, or a scan that hit `ann_max_scan_tuples`) and were answered exactly instead, since the server started; and `exact_scoped`, how many searches were answered exactly without trying the index, because their filter and access admitted at most `ann_exact_scope_docs` documents. A failed index is retried with a growing wait, up to 5 times, then left for you: fix the cause, and clear the model's row in `rag_ann_index` to retry. |

**`error` is a code, never an error's text,** which stays in the log:

| Code | Meaning |
|---|---|
| `database` | the index's database failed |
| `lock_lost` | the tenant's writer lease was lost: taken over, or not renewed in time (the server exits, and its supervisor restarts it) |
| `view` | the view's query failed on your database: a table, a column, a permission |
| `order` | the view's rows aren't in `(updated_at, id)` order ([views](views.md)) |
| `provider_unreachable` | the embedding provider doesn't answer |
| `provider_refused` | the provider refuses the key or the model |
| `provider_rejected` | the provider rejected some texts |
| `other` | anything else: read the log |

- **The figures from the database are read at most every 10 seconds;** `as_of` says when.
- **The lock and the last runs are always current.**

## Metrics in the log

Every `[log] metrics_interval` (1 minute by default; `0` turns them off), the server logs one line for itself, then one per tenant:

```
metrics scope=server provider=up provider_error= http_ok=118 http_4xx=3 http_5xx=0 http_max_ms=412
metrics scope=tenant tenant=music lock=held documents=120344 ready=120344 quarantined=0 semantic=ready cursor_lag_s=4 pass_error= refresh_error= error=
```

- **They're logfmt,** for your log pipeline to parse.
- **The HTTP counts cover the interval:** requests by status class, and the slowest.
- **No line holds a token, a query's text or a client's name.** Each request's own log line names its client.
- **They're logged at `Info`:** a `[log] level` of `notice` or above leaves them out.

## Metrics for a scraper: `GET /v1/metrics`

With `[metrics] enabled = true`, the server serves its figures in Prometheus's text format (0.0.4), for Prometheus, VictoriaMetrics, Grafana Agent or any OpenMetrics-compatible scraper. Off, the path is not found.

```yaml
# prometheus.yml
scrape_configs:
  - job_name: autodoc
    scrape_interval: 30s
    authorization:
      credentials_file: /etc/autodoc/ops.token   # an operator's token
    static_configs:
      - targets: ["127.0.0.1:8730"]
```

- **An operator's token, as `/v1/status`.** A search client's token is refused (403), and a missing or wrong one is refused (401). An operator sees the server's series, and its own tenants' only.
- **The series:**
  - `autodoc_server_info{version}`, `autodoc_provider_up`;
  - With a `[ranker]`: `autodoc_rank_up`; `autodoc_rank_calls_total{result}` (the searches' ranker calls, `ok` or the code of why one failed, a call answered at once while the ranker is down included); `autodoc_rank_seconds` (a histogram of the calls that sent texts); `autodoc_rank_texts_total` (the texts sent, distinct within each call: what a hosted ranker bills); and `autodoc_searches_unranked_total{reason}`.
  - `autodoc_http_requests_total{path, class}` and `autodoc_http_request_duration_seconds{path}` (a histogram). They count since the server started, so take rates of them, and they're never reset by the metrics lines.
  - Per tenant: `autodoc_tenant_documents{tenant, state}` (total, ready), `autodoc_tenant_quarantined`, `autodoc_tenant_cursor_lag_seconds` (`cursor.lag_seconds`: absent on a standby and before its server's first complete pass), `autodoc_tenant_lock_held`, and `autodoc_tenant_errors{tenant, kind}` (pass, refresh, tenant; 0 or 1).
- **`path` is one of search, status, metrics, health and other.** Any other URL is counted as `other`, never by its own name.
- **A tenant whose state can't be read** shows `autodoc_tenant_errors{kind="tenant"} 1` and its lock, and no document or quarantine figures, never a false 0.
- **No series holds a token, a query's text, a client's name, an access value or an error's text.** Errors are 0 or 1 by kind; their text is in `/v1/status` and the log.
- **A scrape is counted once it has answered,** so `path="metrics"` shows the previous scrapes. A scrape interval of 30 s or more is plenty.

## The approximate search's index: what it costs

Each embedding model's HNSW index is built with `postgres.ann_ef_construction` (default 200; see
[server-config.md](server-config.md)). That decides recall more than any search setting: at
pgvector's default of 64, a tenant sharing the index found 0.938 to 0.990 of its exact top ten
across 20 builds, and 8 builds were below 0.95. At 200, every one of 10 builds found 0.990 or more.

The price is paid when the index is built and by every insert while it exists, never by a search.
Measured with `TestANN_Measure` (PostgreSQL 18, pgvector 0.8.7, 16-dimension embeddings, 4 CPUs):

| | 100,000 embeddings | | 1,000,000 embeddings | |
|---|---|---|---|---|
| `ann_ef_construction` | 64 | 200 | 64 | 200 |
| index build | 13 s | 32 s | 14 min | 34 min |
| index size | 35 MB | 35 MB | 351 MB | 351 MB |
| inserts with the index in place | 1,384/s | 784/s | 500/s | 159/s |
| approximate search, p50 (n = 10) | 13.5 ms | 13.4 ms | 22.5 ms | 22.1 ms |

So at a million embeddings, ingest with the index in place is about a third as fast at 200 as at
64. A deployment that values ingest over recall can set a lower `ann_ef_construction`. Changing it
rebuilds each model's index while the old one keeps serving.

**A model's index waits for a tenant's bulk load.** While a tenant whose active model it is
has `postgres.ann_defer_backlog` (2,000) or more documents not yet ready, the model's index is not
built, so the import's embeddings are written without paying the index's per-insert cost. Searches
are exact meanwhile, and the status says `ann-building` with the reason and the time it will be
built by. The index is built once the backlog drains, or `postgres.ann_defer_max` (24 h) after it
first waited, whatever the backlog. A model whose index is built already doesn't wait: a large
import into it still pays the per-insert cost.

Measured with `TestANN_BulkLoadMeasure` (PostgreSQL 18, pgvector 0.8.7, 16-dimension embeddings,
2 CPUs, `--shm-size 1g`): a tenant's documents written first, not ready, then their embeddings in
batches of 20,000, each marking its documents ready.

| | 200,000 documents | | 1,000,000 documents | |
|---|---|---|---|---|
| | index built first | deferred | index built first | deferred |
| import until the index is ready | 5 min 20 s | 51 s | 1 h 21 min | 14 min 39 s |
| embeddings written per second | 624 | 3,938 | 206 | 1,138 |
| recall@10 afterwards | 1.000 | 0.990 | 0.999 | 0.998 |

The deferred times include building the index after the load. The backlog is counted only while a
model's index is wanted: at a full backlog the count stops at `ann_defer_backlog` rows (under 1 ms),
and once drained it reads the partial index of unready documents (0.03 ms at 1,000,000) or, before
the table's statistics catch up, scans it once (26 ms at 200,000).

**Give the database's container shared memory for the build.** The building session uses
`postgres.ann_maintenance_work_mem` (512 MB), and a parallel build keeps it in the database's
`/dev/shm`. Docker gives a container 64 MB by default, so run PostgreSQL with `--shm-size` at
least `ann_maintenance_work_mem` (`--shm-size 1g` for the default; `shm_size: 1g` in Compose).
With less, the build runs out of shared memory, and the server builds the index again without
parallel workers (slower) and logs a warning saying so.

## On Google Cloud: Vertex AI

With `provider = "vertex"` and `kind = "vertex"` ([server-config](server-config.md#vertex-ai-on-google-cloud)),
the server calls Vertex AI and the Ranking API as the VM's service account.

- **The service account.** Attach one to the VM, with the `cloud-platform` access scope: a VM's
  default scopes don't reach Vertex AI. Grant it, in the project:
  - Vertex AI User (`roles/aiplatform.user`), for the embeddings;
  - Discovery Engine User (`roles/discoveryengine.user`), for the ranking.
  Check both against your project once, with `--check`.
- **The APIs.** Enable `aiplatform.googleapis.com` and `discoveryengine.googleapis.com` in the
  project. `--check` names an API that isn't enabled, from Google's answer.
- **The location.** gemini-embedding-2 is served at `global`, `us` and `eu`; pick the one nearest the
  VM. The Ranking API is global.
- **`--check`** fetches a token and probes each, and refuses with what it met: no credentials (no
  service account, no `GOOGLE_APPLICATION_CREDENTIALS`), Google refusing the credentials (a key
  revoked, or a role missing), or an API not enabled. A good probe prints the model and its size.
- **What leaves.** Every chunk embedded and every query go to Vertex AI in the configured location;
  every reranked search sends its query and its window's texts to the Ranking API. Both stay
  within the project's terms with Google.
- **What it costs** (Google's list prices, November 2026; check them before you budget):
  - embedding, gemini-embedding-2: $0.20 per million tokens. A corpus of 11 million tokens costs
    about $2 to embed once, then only what changes, and each query's few tokens.
  - ranking: $1.00 per 1,000 searches of up to 100 candidates (the default window is 40), after a
    free allowance per project a month.
  - A down ranker's probe is a billed search: the server probes it at most once a minute.
  - `autodoc_rank_texts_total` and the ranker's `ranked` count in the status are what the bill
    counts.
- **AlloyDB.** The index can live on the AlloyDB instance your application already uses, in a
  database of its own, while the views read your application's database through a read-only user
  ([server-config](server-config.md#the-index-in-a-database-of-its-own)): `dsn_env` names the
  read-only user's DSN to your database, and `index_dsn_env` a DSN to the index's database on the
  same instance. AlloyDB's `vector` extension is pgvector with Google's additions: Google's
  documentation lists HNSW indexes and the `hnsw.iterative_scan`, `hnsw.max_scan_tuples` and
  `hnsw.scan_mem_multiplier` settings the approximate search uses (vector 0.8.0.google-3 or later).
  It doesn't name `halfvec`, which the server uses for a model above 2,000 dimensions: at 3,072,
  check the index is built (`ann` in the status) before relying on it. Install the extension in the
  index's database once, as the database's owner, and `--check` says what it found.

## Behind a proxy

The server listens on loopback by default. To serve beyond it, either give it a certificate (`tls_cert`, `tls_key`), or put a proxy that terminates TLS in front and say so:

```toml
[http]
listen = "0.0.0.0:8730"
proxy  = true
```

- **`proxy = true` lets it listen off loopback over plain HTTP.**
- **The network between the proxy and the server must be private:** requests cross it in clear text. `--check` warns.
- **It contradicts `tls_cert`;** give one or the other.

## systemd

[`deploy/systemd/autodoc-server.service`](../../deploy/systemd/autodoc-server.service). The binary is
the server build (a `linux-<arch>-server` release archive, or `make build-server`); any other build
refuses `--server`:

```sh
useradd --system --no-create-home --shell /usr/sbin/nologin autodoc
install -m 0755 autodoc /usr/local/bin/autodoc
install -d -m 0750 -g autodoc /etc/autodoc
install -m 0640 -g autodoc server.toml /etc/autodoc/   # and views/, and the TLS certificate and key
install -m 0600 server.env /etc/autodoc/server.env     # AUTODOC_SERVER_DSN=…, the key, the tokens
cp deploy/systemd/autodoc-server.service /etc/systemd/system/
systemctl enable --now autodoc-server
journalctl -u autodoc-server -f
```

- **Secrets live in `server.env`** (0600, root's), never in `server.toml`. systemd reads it as root
  and hands its values to the service; the service itself can't read the file.
- **The files the service reads** (`server.toml`, the views, the TLS certificate and key) are root's,
  0640, group `autodoc`, in a directory of the same: the service reads them through its group, and
  can write none of them.
- **A refused configuration (exit 2) isn't restarted,** because restarting can't mend it. Exit 1 (the database lost, a writer lease lost) is restarted after 5 seconds.
- **On stop:** SIGTERM drains the API's requests (35s at most), then releases the writer leases, so a standby takes them at its next poll. The stop timeout is 60s.
- **The unit runs as the `autodoc` system user,** with the system read-only to it.

### Writer leases

Each tenant's writer is a row of `rag_writer_lease` in the server's schema: who holds it
(`holder_info` names the host, the process id and its start), its epoch, and until when.

```sql
SELECT scope, holder, holder_info, epoch, lease_until FROM <schema>.rag_writer_lease;
```

- **A standby** waits while the lease is held, and takes it over when its holder releases it (a
  clean stop) or 30 seconds after the holder's last renew (a crash, or a host cut off).
- **Taking a tenant over by hand,** for a holder you know is gone or must stop, is one statement:
  the release's own, without its holder check. It clears the holder and moves the epoch, so the
  holder's next renew and next write both find the lease lost (it exits 1), and a standby claims
  it at its next poll after any write the holder already has open has ended:

  ```sql
  UPDATE <schema>.rag_writer_lease SET holder = '', epoch = epoch + 1, lease_until = clock_timestamp()
   WHERE scope = 'tenant:<name>';
  ```

  A write the holder already has open still finishes: the statement doesn't wait for it. Only the
  holder's next transaction is refused. Setting `lease_until` alone is not a takeover: a live
  holder's next renew extends it again.
- **The `lag` of a source** must be longer than the longest transaction that writes its rows: a
  row committed later than `lag` after its `updated_at` is passed over by the incremental passes and
  found only by the next refresh. Find your longest with `SELECT max(clock_timestamp() - xact_start)
  FROM pg_stat_activity WHERE state <> 'idle'` on the business database, under load.

### Upgrading from v0.3.15

**Stop every v0.3.15 server of a store before you start a v0.3.16 one.** A v0.3.15 server held each
tenant by a PostgreSQL advisory lock, which the lease knows nothing of, so the two would write at once.

- A v0.3.15 server refuses to start on a store a newer release has migrated: it says the
  database has a newer schema. So only one already running when the store was migrated can meet
  a newer server.
- A v0.3.16 server looks for that lock before it takes a tenant's lease, and again at every renew.
  - While a v0.3.15 server holds the tenant, it waits and logs that an older server holds it, and
    the status shows `"lock": "waiting"` with `"lock_wait": "older_server"`.
  - If an older server takes the lock while this one holds the lease (a v0.3.15 standby, already
    waiting when you upgraded), this one stops writing and exits 1, and its supervisor restarts it
    to wait.
- From v0.3.16 on, upgrades are rolling: every server takes the same lease, and a standby of the
  new release takes over as the old one stops.

## A container

[`deploy/container/Dockerfile`](../../deploy/container/Dockerfile) builds the server build, a static binary, onto a distroless image, which has no shell and no curl. Its healthcheck is autodoc's own probe:

```sh
docker build -f deploy/container/Dockerfile -t autodoc-server .
docker run -d --env-file .env -p 127.0.0.1:8730:8730 \
  -v "$PWD/server.toml:/etc/autodoc/server.toml:ro" -v "$PWD/views:/etc/autodoc/views:ro" \
  autodoc-server
```

[`deploy/container/compose.yml`](../../deploy/container/compose.yml) runs it beside PostgreSQL with pgvector (and Ollama, with `--profile ollama`), using [`deploy/container/server.toml`](../../deploy/container/server.toml) and `deploy/views/`. Its `.env` holds `POSTGRES_PASSWORD` too, which the database is created with and `AUTODOC_SERVER_DSN` names. The systemd unit's [`deploy/server.toml`](../../deploy/server.toml) listens on `127.0.0.1` only; the container's copy differs in that one line, listening on the container's own network.

**Publish the port on loopback only.** The example's `server.toml` sets `proxy = true`, so the API in the container is plain HTTP, and the bearer tokens cross the wire in clear text. Both the `docker run` above and the compose file publish `127.0.0.1:8730`.
- Put your TLS-terminating proxy (Caddy, nginx, a load balancer's agent) on the same host, in front of it.
- Never publish `8730` on every interface.
- Or set `tls_cert` and `tls_key` and drop `proxy`, and the server serves HTTPS itself.

### `autodoc --server --health`

```sh
autodoc --server --health /etc/autodoc/server.toml
```

It asks the server described by that configuration for its health, prints the answer, and exits 0 for ok or degraded, 1 otherwise. That includes a configuration with no `[http]`, which has no health to ask.

- **The address** is `[http] listen`'s own host, or loopback when the server listens on every address. The probe runs where the server does.
- **TLS:** with `tls_cert` set, it uses HTTPS and trusts exactly the certificate the server serves: the first one in that file. An intermediate after it is not trusted on its own. The name it checks is that certificate's first DNS name, or its first IP address. It never skips verification.
- **The timeout is 5 seconds.**
