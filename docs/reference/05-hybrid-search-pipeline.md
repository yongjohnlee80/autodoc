# Chapter 5: The Hybrid Search Pipeline

AutoDoc is engineered around a core retrieval thesis: neither lexical keyword matching nor dense vector similarity is sufficient on its own for technical documentation. Pure keyword search (`rg` or unranked BM25) fails when an author writes *"we chose an embedded relational store"* and a reader searches for *"why did we pick SQLite"*. Conversely, pure vector search struggles with exact error codes (`-32069`), function names (`reclaimBatch`), or short hexadecimal commit prefixes.

To bridge both regimes without sacrificing interactive latency, AutoDoc (`core/index`) implements a three-stage hybrid retrieval funnel:

1. **Stage 1 — Lexical Retrieval (`StageLexical`):** SQLite FTS5 BM25 over heading-scoped chunks with structural field weights, schema facet pre-filtering, tag boosts, and inbound link authority boosts.
2. **Stage 2 — Semantic Recall (`StageSemantic`):** Two-step vector search combining a 1-bit quantized binary Hamming prefilter over all workspace chunks with exact `float32` cosine similarity rescoring, merged with Stage 1 via Reciprocal Rank Fusion (RRF).
3. **Stage 3 — Cross-Encoder Reranking (`StageRerank`):** Joint query-passage scoring (`core/rank`) over the top candidate window (`10..100`, default `40`) using a local HuggingFace Text Embeddings Inference (TEI) server or a Cohere-compatible rerank API.

![AutoDoc's three-stage hybrid retrieval funnel from query parsing to ranked line spans](three-stage-retrieval-funnel.mmd)

---

## 1. Heading-Scoped Chunking and `section_tokens`

Before any retrieval stage executes, AutoDoc cuts each indexed document into semantically coherent chunks rather than arbitrary fixed-character sliding windows.

### The Index in the Store

The index is in the store, one SQLite file for every workspace, with each workspace's rows keyed by it. One connection writes, and any number read, each from one snapshot:

- **An edit writes only the chunks it changed:** each document has generations, and a reader sees the old one or the new one, never a mix.
- **Frontmatter is metadata:** the title, tags and aliases. Inline `#tags` count too. A frontmatter schema types it ([Chapter 7](./07-frontmatter-schemas-and-validation.md)).
- **A change log feeds clients' incremental sync.** It is kept for at least 7 days and 100 000 changes.

### Markdown Section Splitting

When the indexer (`core/index`) processes a Markdown file, it parses the heading hierarchy (`# H1` through `###### H6`) and splits the body at heading boundaries:

- **Target Chunk Size (`section_tokens`):** Each workspace configures a target section budget (default `350` estimated tokens, configurable via `workspace.section_size` or `workspace.configure` with `section_tokens`). Sections smaller than the budget remain a single chunk; oversized sections are subdivided along paragraph boundaries while preserving their parent heading breadcrumb.
- **Structural Breadcrumbs:** Every chunk stores its full ancestor path in `Hit.Breadcrumb` (for example, `ADR 0203 › 4. Storage Engine › 4.4 Search Constants`). Both the lexical index and the embedding text prepend this breadcrumb so a subsection titled simply `### Trade-offs` retains the topical context of its parent document.
- **Synthesized Abstract Chunks (`abstract_chunk`):** When a document's YAML frontmatter includes an `abstract` field (required by AutoDoc's v2 KB schema), the indexer synthesizes a dedicated abstract chunk representing the document's high-level thesis. Queries can include or exclude these abstract chunks on the fly via `retrieval.abstract_chunk`.
- **Exact Line and Byte Spans:** Every chunk records both its byte offsets (`byte_start..byte_end`) and 1-based line numbers (`line_start..line_end`) alongside the document's monotonic `generation`. Clients and AI agents can read the exact matched section (typically ~220 tokens) rather than pulling an entire 2,600-token file into context.
- **Content-Addressed Deduplication:** Chunk embeddings are keyed in SQLite by `(workspace, model_fp, text_hash)`. Editing a single paragraph in a 20-section specification re-embeds only the modified section; identical boilerplate sections across files share a single vector row.

### Configuring `section_tokens` per Workspace

Workspaces holding dense API reference tables often benefit from tighter chunks (`250` tokens), whereas narrative design documents or RFCs read better at `450`–`500` tokens:

```sh
# Adjust the target section chunk size for workspace "kb" to 400 tokens
autodoc --call workspace.section_size '["kb", 400]'

# Or configure section_tokens alongside retrieval policies in one atomic call
autodoc --call workspace.configure '[
  "kb",
  {
    "section_tokens": 400,
    "abstract_chunk": true,
    "demote_superseded": true
  }
]'
```

---

## 2. Stage 1: Lexical FTS5 BM25, Facets, and Authority Boosts

Stage 1 (`StageLexical`) runs entirely inside SQLite using FTS5 with Porter stemming and BM25 ranking. It requires zero external model servers and answers in single-digit milliseconds.

### Safe Word and Phrase Parsing

AutoDoc intentionally treats every user query as plain words and phrases rather than raw FTS5 syntax, preventing syntax errors when searching for punctuation-heavy code or log snippets:

- **Stemmed Words:** `sqlite store` matches chunks containing both tokens, stemmed (`stores`, `storing`, `stored`).
- **Exact Phrases:** `"workspace manager"` matches the tokens adjacent and in order.
- **Trailing Prefix Wildcard:** A `*` at the end of the final word (`migr*`) performs a prefix scan.
- **Literal Operators:** Reserved FTS5 keywords (`AND`, `OR`, `NOT`, `NEAR`, column filters) are escaped and searched as ordinary words.

### Weighted Column Scoring and Frontmatter Boosts

Within the FTS5 index, matches in a document's title, heading breadcrumb, frontmatter aliases, and tags carry higher BM25 weights than body prose. Up to `retrieverTop = 50` lexical candidates (`core/index/search.go`) advance to the fusion step.

---

## 3. Stage 2: 1-Bit Hamming Prefilter and Float32 Cosine Recall

When an embedding provider is active and ready, Stage 2 (`StageSemantic`) embeds the query string (minus any stripped `field:value` facet filters) into a normalized `float32` vector and executes a two-phase scan over the workspace's chunk vectors (`golib/search/vector`):

1. **Phase 2a — 1-Bit Quantized Binary Scan:** Alongside every `float32` embedding (`f32_bytes`), AutoDoc stores a compact 1-bit sign-quantized binary code (`bits_bytes` — 32x smaller than `float32`). For a 1,024-dimensional embedding, the binary code occupies just 128 bytes per chunk. The engine first computes XOR + hardware `popcnt` Hamming distance across the workspace's binary codes to rapidly shortlist the most promising semantic candidates.
2. **Phase 2b — Exact Float32 Cosine Rescoring:** The shortlisted chunks are rescored using exact `float32` dot-product cosine similarity, yielding the top `retrieverTop = 50` semantic candidates.

Because embedding is asynchronous and per-document, a file that is only halfway embedded continues to answer lexically until every chunk in that document has its vector, and `Result.Semantic` reports `"partial"` so callers know semantic coverage is still warming.

---

## 4. Reciprocal Rank Fusion (RRF) and Graph/Metadata Boosts

Lexical BM25 scores (unbounded positive floats) and cosine similarities (`[-1, 1]`) live on incompatible numerical scales. AutoDoc merges the top 50 lexical candidates and top 50 semantic candidates using **Reciprocal Rank Fusion** with constant $k = 60$ (`rrfK = 60` in `core/index/search.go`):

$$\text{RRF}(c) = \sum_{r \in \{\text{lexical}, \text{semantic}\}} \frac{1}{60 + \text{rank}_r(c)}$$

After summing RRF contributions across every retriever that found chunk $c$, AutoDoc applies four structural adjustments (`core/index/search.go`):

- **Tag Match Boost (`tagBoost = 1.2`):** Chunks belonging to a document whose YAML `tags` or inline `#tags` match a query token receive a `1.2` boost multiplier.
- **Inbound Link Authority Boost (`linkBoost = 0.1`):** Documents referenced by other notes in the workspace knowledge graph receive an authority boost proportional to their resolved inbound backlinks.
- **Normalized Relevance (`0.0..1.0`):** `Hit.Relevance` projects `Score` onto a fixed scale where `1.0` represents ranking #1 in every active retriever (`1 / (60 + 1)` each) prior to boosts, capped at `1.0`. A chunk found by only one retriever scores at most ~`0.5` unless boosted by tags or inbound links.
- **Per-Document Diversity Cap (`perDocument = 3`):** At most three chunks from any single file are retained so a long reference manual cannot crowd out other documents.
- **Superseded Document Demotion (`demote_superseded`):** When enabled (the workspace default), if both an older ADR (`status: superseded`, or linked via `supersedes` / `superseded_by`) and its active successor appear in the results, the superseded document's hits are moved directly below the successor's hits and annotated with `Hit.SupersededBy`.

---

## 5. Stage 3: Cross-Encoder Reranking (`core/rank`)

Bi-encoder embeddings compress an entire chunk into a single vector independent of the query. Stage 3 (`StageRerank`, ADR 0215) passes the top fused window (`ranker.window`, default `40`, configurable from `10` to `100`) to a **cross-encoder reranker** (`core/rank`).

The cross-encoder reads the raw query string side by side with each candidate chunk's breadcrumb and body text, computing full cross-attention across query and document tokens to emit a calibrated logit (`Hit.RankScore`). Hits in the top window are re-sorted by `RankScore` descending, and `"rank"` is appended to `Hit.Via`.

- **The window.** A wider window ranks deeper and takes longer. On a CPU, `bge-reranker-v2-m3` takes about a quarter of a second a text as an ONNX export and about half a second as is, so give a CPU ranker 10 to 20.
- **The hits say so.** Each ranked hit has a `rank_score`, the answer's `rank` names the model, and the TUI's hits title says `re-ranked by <model>` or `not re-ranked: <why>`. The search picker's Rerank checkbox names the ranker in use as soon as it opens; unchecked, the hits are in the order they were found.
- **A search that leaves `rerank` out is never ranked**, even under a build that answers ranked or not at all, so a client can compare the two.
- **A ranker that does not answer leaves the hits in the order they were found**, and the answer's `rank` says why. Its calls are metered like a provider's.

![Benchmark comparison of Top-3 Recall across Keyword Grep, Lexical BM25, Hybrid RRF, and Full 3-Stage Reranking](retrieval-accuracy-by-stage.chart.json)

As measured in `docs/benchmarks/kb-retrieval.md` across production engineering knowledge bases (2,200 documents and 32 real agent lookups), adding semantic recall lifts Top-3 recall from **76%** (lexical BM25) to **90%** (hybrid), and adding the cross-encoder reranker (`BAAI/bge-reranker-v2-m3`) reaches **98%** Top-3 recall—allowing agents and users to set `limit: 3` and cut context token consumption by 60% compared to `limit: 10`.

![Latency breakdown across Lexical FTS5, Vector Cosine, and Cross-Encoder Rerank stages](query-latency-breakdown.chart.json)

---

## 6. Query Filter Grammar and `search.query` Options

When a workspace has an active frontmatter schema (`_schema/frontmatter.yaml`), every declared field in `common` and `types.<name>` automatically becomes an indexed facet that filters candidates *before* ranking limits apply.

### Inline Facet Syntax and Negation

- **Positive Facets (`field:value`):** `migration type:adr status:accepted` strips `type:adr` and `status:accepted` from the text query, restricts the candidate set to accepted ADRs, and ranks matches for `migration`. Values are coerced to the schema field's declared type (`integer`, `date`, `boolean`, `string`).
- **Negated Facets (`-field:value`):** Prefixing a declared facet token with `-` excludes matching documents—for example, `consensus -status:superseded -status:deprecated` filters out retired decisions before retrieval.
- **Tag Shorthand (`tag:<name>`):** Filters documents carrying `<name>` in frontmatter `tags:` or inline `#<name>`.
- **Wordless Facet Browsing:** A query consisting solely of facet tokens (`type:adr status:proposed`) skips embedding and reranking altogether, returning matching files in path order with `mode_used: "facet"`.
- **Undeclared Fields Fall Back to Words:** If `foo:bar` names a field not declared in the workspace schema, AutoDoc never errors on the inline token—it searches `foo:bar` as literal words. (By contrast, passing an undeclared field in the structured `opts.facets` map returns `-32602 rpc.invalid_params`.)

```sh
# 1. Full 3-stage hybrid search scoped to ADRs and synthesis docs, excluding superseded records
autodoc --call search.query '[
  "kb",
  "lease recovery lock -status:superseded",
  {
    "limit": 5,
    "paths": ["adrs", "synthesis"],
    "tags": ["sqlite"],
    "facets": {"type": ["adr", "synthesis"]},
    "retrieval": {"abstract_chunk": true, "demote_superseded": true}
  }
]'

# 2. Compare Lexical + Semantic without Stage 3 reranking
autodoc --call search.query '[
  "kb",
  "lease recovery lock",
  {"stages": ["lexical", "semantic"], "limit": 5}
]'

# 3. Read only the matched section line span (lines 41..77) from the top hit
sed -n '41,77p' /path/to/kb/adrs/0203-storage-lease.md
```

![Complete reference of AutoDoc search query operators, facet filters, options, and response fields](query-grammar-reference.csv)

---

## 7. Honest Stage Reporting (`stages.requested`, `performed`, `skipped`)

Distributed systems and AI agents need to know whether a search result reflects full semantic + cross-encoder understanding or a fallback to words-only BM25 during a model switch. A search can ask for its stages: `lexical` (words), `semantic` (meaning) and `rerank` (the ranker), at least one of the first two; asked for none, it runs them all. Every `search.query` response includes a deterministic `stages` record (`core/index/stages.go`) alongside `mode_used`, `semantic`, and `rank`:

```json
{
  "hits": [
    {
      "path": "adrs/0203.md",
      "breadcrumb": "ADR 0203 › Storage",
      "snippet": "The store takes an exclusive lease beside autodoc.db...",
      "byte_start": 1204,
      "byte_end": 2310,
      "line_start": 41,
      "line_end": 77,
      "relevance": 0.94,
      "score": 0.031,
      "via": ["lexical", "rank"],
      "rank_score": 0.87,
      "generation": 7,
      "hold": ""
    }
  ],
  "mode_used": "lexical",
  "semantic": "switching",
  "rank": {"state": "ready", "model": "BAAI/bge-reranker-v2-m3", "error": ""},
  "stages": {
    "requested": ["lexical", "semantic", "rerank"],
    "performed": ["lexical", "rerank"],
    "skipped": {
      "semantic": "a new embedding model is filling"
    }
  }
}
```

### Stage Skip Reasons (`core/index/stages.go`)

| Stage | Skip Constant | Exact Message in `stages.skipped[<stage>]` |
|---|---|---|
| Any stage | `SkipNoWords` | `"the query has no words"` |
| `semantic` | `SkipNoModel` | `"no embedding model is in use"` |
| `semantic` | `SkipPaused` | `"the workspace's embedding policy pauses semantic search"` |
| `semantic` | `SkipSwitching` | `"a new embedding model is filling"` |
| `semantic` | `SkipEmbedFailed` | `"the query could not be embedded"` |
| `rerank` | `SkipNoRanker` | `"no ranker is in use"` |
| `rerank` | `SkipRankFailed` | `"the ranker could not rank this search"` |

### Interactive Stage Toggles in the TUI and Neovim

The TUI's pickers (search, open a document, add a workspace) share one layout: the fields over the list on the left, the file under the cursor on the right, the buttons beneath. The search (`Ctrl+G` or `SPC /`) runs as it is typed and refreshes its open query after a model or workspace transition; its preview is at the hit, the words marked. Under it, a checkbox for each stage the search can run: **Lexical**, **Semantic** while an embedding model is in use, and **Rerank (<model>)** while a ranker is. All start checked; each change searches again at once, so flipping them shows what each stage changes. One of Lexical and Semantic always stays checked. The choice is kept, and a box that hides while its stage is unavailable comes back as it was left.

In Neovim (`:AutodocSearch`), `<M-l>`, `<M-s>`, and `<M-r>` toggle these three stages live, and each flip re-runs the query.

If a caller explicitly requests semantic-only retrieval (`"stages": ["semantic"]` or `"mode": "semantic"`) while no embedding model is available or a model switch is in progress, AutoDoc refuses to silently substitute lexical hits—it returns an explicit RPC error (`-32065 rpc.unsupported`, `-32067 rpc.embed_failed`, or `-32069 rpc.switching`). When `"lexical"` is included alongside `"semantic"` (the default), AutoDoc gracefully answers via `lexical` and records the exact skip reason in `stages.skipped.semantic`.

---

[← Previous: Chapter 04: Rich Authoring, Embeds & Export](./04-rich-authoring-embeds-and-export.md) | [Index](./README.md) | [Next: Chapter 06: Configuring Embeddings & Rerankers →](./06-configuring-embeddings-and-rerankers.md)

