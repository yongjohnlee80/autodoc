# Chapter 19: Enterprise Postgres RAG Server

Chapters 1 through 17 focused on indexing local Markdown workspaces on an individual engineer's workstation. When your searchable knowledge lives inside **relational database tables**—such as a multi-tenant music or product catalog, customer support tickets, legal contracts, or clinical records—and hundreds of web application users need to search that data by both keywords and semantic meaning under strict row-level authorization, AutoDoc provides **`autodoc --server`** (`docs/enterprise/*`, `core/pgstore`, `enterprise/search`).

`autodoc --server` is a single headless Go binary that reads your PostgreSQL business tables through declarative `.view` contracts, renders each row into Markdown, chunks it using AutoDoc's heading-aware splitter, stores the index in a dedicated schema (`autodoc_search`) alongside your tables using PostgreSQL + `pgvector`, embeds distinct chunk hashes centrally, and serves multi-tenant hybrid RAG queries over `POST /v1/search` (or in-process via the `enterprise/search` Go library).

It is in the server build only (a `linux-<arch>-server` release archive, `make build-server`, or `-tags server`).

[`docs/enterprise/`](../enterprise/) has all of it: [what it is and when to use it](../enterprise/README.md), [the server configuration](../enterprise/server-config.md), and [writing a `.view` source](../enterprise/views.md).

---

## 19.1 Architecture Overview (`core/pgstore` & `enterprise/*`)

Unlike external vector databases that force you to replicate business data across network boundaries and reconcile eventual-consistency drift, `autodoc --server` keeps the search index inside your existing PostgreSQL database in an isolated schema (`autodoc_search` by default) while never writing to your business tables.

![Enterprise Postgres RAG Server Architecture](enterprise-server-architecture.mmd)

The server maintains three specialized PostgreSQL connection pools and four core subsystems:

1. **Declarative `.view` Ingesters (One per Source/Tenant)**: Each `[[source]]` in `server.toml` maps one `.view` file to an isolated `tenant`. The ingester acquires a dedicated PostgreSQL session-level **advisory lock** for that tenant (`lock = "held"`), ensuring that even if you deploy multiple `autodoc --server` replicas for high availability, at most one writer mutates a tenant at a time while standby instances wait seamlessly for failover.
2. **Storage Tables (`core/pgstore`)**: Inside `autodoc_search`, the server manages document records (`rag_document` / `documents`), full-text search tsvector chunks (`rag_chunk` / `chunks_fts`), deduplicated vector embeddings (`rag_embedding` / `content_vectors`), keyset ingestion cursors (`rag_cursor`), quarantined row errors (`rag_ingest_error`), and immutable tenant access policies (`rag_tenant_policy`).
3. **Central Distinct-Hash Embedding Worker**: A single background worker across all tenants batches unembedded chunk texts (64 texts per batch across `slots = 4` concurrent requests) and embeds each distinct SHA-256 content hash once per tenant.
4. **Snapshot-Scoped Search (`POST /v1/search` & `enterprise/search`)**: Every search query runs inside a `REPEATABLE READ` transaction that validates the caller's mandatory access `scope` against the tenant policy in the same snapshot before executing hybrid lexical + `pgvector` retrieval.

---

## 19.2 Declarative `.view` Contracts and Keyset Cursor Pagination

A `.view` file combines YAML frontmatter with a Go `text/template` Markdown body. To guarantee that incremental passes never skip rows—even when hundreds of rows share the exact same timestamp—every source view must declare `args: [after_ts, after_id, upto_ts]` and order by the composite row tuple `(updated_at, id)`:

```yaml
---
version: 1
name: track
source: postgres
args: [after_ts, after_id, upto_ts]
process: |
  SELECT jsonb_build_object(
    'id',         t.id,
    'label_id',   t.label_id,
    'territory',  t.territory,
    'title',      t.title,
    'artist',     a.name,
    'lyric',      t.lyric,
    'updated_at', GREATEST(t.updated_at, a.updated_at))
  FROM track t
  JOIN artist a ON a.id = t.artist_id
  WHERE (GREATEST(t.updated_at, a.updated_at), t.id) > ($1, $2)
    AND GREATEST(t.updated_at, a.updated_at) <= $3
  ORDER BY GREATEST(t.updated_at, a.updated_at), t.id
  LIMIT 500
export: track/{{.id}}.md
---
# {{.title}}
by {{.artist}}

{{if .lyric}}{{.lyric}}{{else}}*(Instrumental)*{{end}}
```

### Keyset Invariants, Lag Bounds, and Quarantine

- **Composite Keyset Comparison (`($1, $2)`)**: `$1` is `after_ts` and `$2` is `after_id` (`NULL` on the very first page). Comparing `(updated_at_expr, id) > ($1, $2)` ensures that a page boundary splitting a tie of identical `updated_at` timestamps resumes at the exact next `id`.
- **Clock-Skew & Long-Transaction Lag (`$3 = now() - lag`)**: Incremental passes run every `interval` (`1m` default) up to `now() - lag` (`1m` default) so transactions that started slightly earlier than a committed row do not slip behind the cursor. Every `refresh` period (`24h` default, with `refresh_retry = "15m"`), a full pass sweeps all rows to detect content changes missed by timestamps and prune deleted rows.
- **Page-Atomic Transactions and Quarantine**: Each 500-row page is committed in a single PostgreSQL transaction together with the updated `(updated_at, id)` cursor. A crash mid-pass replays at most one page. Any row missing a required `access` field or failing template rendering is recorded in `rag_ingest_error` (quarantine) and retried on subsequent passes rather than halting ingestion or indexing unscoped data.
- **Pre-Deployment Verification (`--check`)**: Running `autodoc --server --check server.toml` validates `server.toml` (exiting `2` on any unknown key or plain-text secret), executes each `.view` read-only over up to 2,000 live rows in a single snapshot, tests up to 200 page-boundary and timestamp-tie resumptions, and sends a probe text to the embedding provider.

---

## 19.3 Central Embedding and Zero-Downtime Model Switching

Ingestion never blocks on the embedding provider. As soon as an ingester commits a page, those documents are immediately searchable by lexical keywords (`tsvector` BM25), and the commit wakes the central embedding worker.

- **Deduplicated `content_vectors`**: If 10,000 tracks share an identical boilerplate licensing section or `*(Instrumental)*` chunk, the worker embeds that chunk text's hash **once** and links all 10,000 chunks to the normalized vector. Once all chunks of a document have vectors under the active model, the document is marked ready for semantic search.
- **Zero-Downtime Model Switching**: If you change `[embedding] model` in `server.toml` and restart, the server keeps the old model active while backfilling vectors for the new target model in the background (`semantic: "switching"`). Once 100% of documents are embedded under the new model, the tenant atomically flips its active model pointer and purges the old vectors.

---

## 19.4 Scoped Search (`POST /v1/search`), Highlighting, and Operations

Every `[[source]]` declares mandatory `access` fields (such as `access = ["label_id", "territory"]`). Neither `POST /v1/search` nor the `enterprise/search` Go library permits an unscoped query.

```toml
# /etc/autodoc/server.toml
[postgres]
dsn_env = "AUTODOC_SERVER_DSN"
schema  = "autodoc_search"

[embedding]
provider = "openai"
base_url = "http://127.0.0.1:8080/v1" # Self-hosted TEI or OpenAI endpoint
model    = "BAAI/bge-base-en-v1.5"
key_env  = "AUTODOC_EMBED_KEY"
slots    = 4

[http]
listen = "127.0.0.1:8730"

[[http.client]]
name         = "catalog-web"
token_sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
[http.client.grants.lm-catalog]
label_id  = ["*"]
territory = ["US", "GB", "JP"]

[[http.operator]]
name      = "sre-monitor"
token_env = "AUTODOC_OPS_TOKEN"
tenants   = ["*"]

[[source]]
tenant   = "lm-catalog"
view     = "views/track.view"
interval = "1m"
refresh  = "24h"
lag      = "1m"
access   = ["label_id", "territory"]
```

### Snapshot-Enforced Access Scopes and Highlighting

When a web application calls `POST /v1/search`, the server verifies the bearer token against `[[http.client]]` (stored only as a 64-hex-digit `token_sha256` or read from `token_env`), confirms every requested scope value is permitted by `grants.<tenant>.<field>`, and checks the tenant policy inside the search transaction's `REPEATABLE READ` snapshot:

```sh
curl -s http://127.0.0.1:8730/v1/search \
  -H "Authorization: Bearer $CATALOG_WEB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant": "lm-catalog",
    "query": "midnight acoustic",
    "scope": {"label_id": ["label-17", "label-42"], "territory": ["US"]},
    "group": "document",
    "limit": 20
  }'
```

Within each scope field, any listed value matches (`OR`); across distinct scope fields, every field must match (`AND`). Any query or filter attempting to reference a protected access facet (`label_id:other`) is rejected with `400 protected_facet` (`search.ErrProtectedFacet`). Matched snippet terms are delimited by control characters `\u0002` and `\u0003` so web frontends can HTML-escape every text segment before wrapping matches in `<mark>` tags, preventing XSS injection.

---

## 19.5 In-Process Go Integration (`enterprise/search`) and Operator Telemetry

If your web application is written in Go, you do not even need to make an HTTP hop to `POST /v1/search`: you can run `autodoc --server` without an `[http]` block for background ingestion and embedding while importing `github.com/yongjohnlee80/autodoc/enterprise/search` directly into your API server:

```go
package catalog

import (
	"context"

	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

func SearchTracks(ctx context.Context, searcher *search.Searcher, labelIDs []string, q string) (search.Response, error) {
	return searcher.Query(ctx, "lm-catalog", q, search.Options{
		Scope: map[string][]string{
			"label_id":  labelIDs,
			"territory": {"US", "CA"},
		},
		Group: search.GroupDocument,
		Limit: 25,
	})
}
```

### Operator Telemetry (`/v1/status` and `/v1/health`)

Operators authenticated via `[[http.operator]]` can query `GET /v1/status` (or `GET /v1/status?tenant=lm-catalog`) to inspect per-tenant ingestion health, advisory lock ownership (`lock: "held"` vs. `"waiting"`), last cursor `(updated_at, id)`, semantic embedding progress (`ready_documents`, `pending_documents`, `semantic: "ready" | "backfilling" | "switching"`), and the count of quarantined rows in `rag_ingest_error`. Load balancers and Kubernetes probes can poll `GET /v1/health` (unauthenticated liveness check) to verify database pool reachability.

---

## 19.6 Self-Hosted TEI Reranking and Illustrative Cost Model

Because `autodoc --server` speaks the standard OpenAI `/v1/embeddings` and HuggingFace Text Embeddings Inference (**TEI**) contracts over HTTP, organizations can run `autodoc --server` alongside self-hosted TEI embedding and cross-encoder reranking containers on fixed-cost instances rather than paying per-query managed cloud ranking API fees.

![Illustrative Monthly Cost Model: Fixed Self-Hosted TEI vs Per-Query Rerank API](enterprise-rag-tco-comparison.chart.json)

In the **illustrative deployment scenario** modeled above—assuming a fixed self-hosted stack (one dedicated PostgreSQL/`pgvector` instance at ~$220/mo plus one GPU/CPU TEI inference node at ~$151/mo = **$371/month flat**) versus a managed cloud vector database base tier (~$515/mo) plus a metered cross-encoder reranking API billed at **$1.00 per 1,000 queries**—total monthly cost stays flat as query volume grows from 200k to 1M queries/month because self-hosted TEI inference incurs no per-request API surcharge.

![Enterprise .view Directive, server.toml, and HTTP API Reference](view-file-directive-reference.csv)

---

[← Previous: Chapter 18: Neovim Integration & KB Migration](./18-neovim-integration-and-kb-migration.md) | [Index](./README.md)

