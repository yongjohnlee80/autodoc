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
| `database` | a ping answers within 2s | | it doesn't |
| `tenants` | every tenant's writer lock is held | a lock is held by another server (this one is a standby) | |
| `embedding` | the provider is up | it's down or failing: searches answer by words meanwhile | |

- **`status`:**
  - `ok`;
  - `degraded`: searches still answer, by words or for the other tenants;
  - `unavailable`: the database fails, or the server is stopping.
- **HTTP status:** 200 for ok and degraded, 503 (with `Retry-After`) for unavailable. Keep a degraded server in rotation: it still answers.
- **Every request is fresh.** It pings the database itself; nothing is cached.

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
            "embedding": {"model": "openai|text-embedding-3-small||1536", "state": "up", "error": ""}},
 "tenants": [{"tenant": "music", "lock": "held",
   "cursor": {"updated_at": "2026-10-13T09:41:03Z", "id": "8812", "lag_seconds": 4},
   "last_pass": {"at": "…", "read": 12, "written": 12, "quarantined": 0, "error": ""},
   "last_refresh": {"at": "…", "complete": true, "last_complete": "…", "error": ""},
   "quarantine": 0,
   "documents": {"total": 120344, "ready": 120344},
   "models": {"active": "openai|…", "target": "", "semantic": "ready", "search": "ann"},
   "error": ""}],
 "ann": [{"model": "openai|text-embedding-3-small||1536", "mode": "ann", "underfill": 3}]}
```

| Field | What it tells you |
|---|---|
| `lock` | `held`; `waiting` (another server holds the tenant: a standby); `none` |
| `cursor.lag_seconds` | how far the index is behind your data, on the database's clock. It grows when passes fail or can't keep up. |
| `last_pass`, `last_refresh` | when each last ran, what it read and wrote, and its error code. `complete` is false for a refresh that couldn't read every row (it deletes nothing then; see [views](views.md)). |
| `quarantine` | rows that failed to render and wait to be fixed in your data (the log names them) |
| `documents` | the tenant's documents, and how many can be found by meaning |
| `models.semantic` | `ready`, `partial` (embedding), `switching` (a model change filling; searches answer by words), or `off` |
| `models.search`, `models.search_reason` | how the tenant's searches by meaning run: `exact` (below `ann_threshold` embeddings, or a model with no index; the reason says which), `ann` (approximate, through the model's HNSW index), `ann-building` (the index is being built; exact meanwhile), or `ann-failed` (exact; the reason is the build's error) |
| `ann` | each embedding model's index: its `mode`, the `reason` when it failed, and `underfill`, how many approximate searches came back short (a selective filter, or a scan that hit `ann_max_scan_tuples`) and were answered exactly instead, since the server started. A failed index is retried with a growing wait, up to 5 times, then left for you: fix the cause, and clear the model's row in `rag_ann_index` to retry. |

**`error` is a code, never an error's text,** which stays in the log:

| Code | Meaning |
|---|---|
| `database` | the index's database failed |
| `lock_lost` | the tenant's writer lock was lost (the server exits, and its supervisor restarts it) |
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
  - `autodoc_http_requests_total{path, class}` and `autodoc_http_request_duration_seconds{path}` (a histogram). They count since the server started, so take rates of them, and they're never reset by the metrics lines.
  - Per tenant: `autodoc_tenant_documents{tenant, state}` (total, ready), `autodoc_tenant_quarantined`, `autodoc_tenant_cursor_lag_seconds` (absent until the cursor first moves), `autodoc_tenant_lock_held`, and `autodoc_tenant_errors{tenant, kind}` (pass, refresh, tenant; 0 or 1).
- **`path` is one of search, status, metrics, health and other.** Any other URL is counted as `other`, never by its own name.
- **A tenant whose state can't be read** shows `autodoc_tenant_errors{kind="tenant"} 1` and its lock, and no document or quarantine figures, never a false 0.
- **No series holds a token, a query's text, a client's name, an access value or an error's text.** Errors are 0 or 1 by kind; their text is in `/v1/status` and the log.
- **A scrape is counted once it has answered,** so `path="metrics"` shows the previous scrapes. A scrape interval of 30 s or more is plenty.

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
install -m 0755 autodoc /usr/local/bin/autodoc
install -d -m 0750 /etc/autodoc
install -m 0640 server.toml /etc/autodoc/          # and views/
install -m 0600 server.env /etc/autodoc/server.env # AUTODOC_SERVER_DSN=…, the key, the tokens
cp deploy/systemd/autodoc-server.service /etc/systemd/system/
systemctl enable --now autodoc-server
journalctl -u autodoc-server -f
```

- **Secrets live in `server.env`** (0600), never in `server.toml`.
- **A refused configuration (exit 2) isn't restarted,** because restarting can't mend it. Exit 1 (the database lost, a writer lock lost) is restarted after 5 seconds.
- **On stop:** SIGTERM drains the API's requests (35s at most), then releases the writer locks. The stop timeout is 60s.
- **The unit runs as a dynamic user,** with the system read-only to it.

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
