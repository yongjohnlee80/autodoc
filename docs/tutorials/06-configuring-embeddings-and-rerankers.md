# Chapter 6: Configuring Embeddings and Rerankers

AutoDoc separates its always-on lexical index from its optional neural inference tiers: dense bi-encoder embeddings (`core/embedding`) and cross-encoder rerankers (`core/rank`). Because both tiers are managed dynamically by the daemon and persisted in the SQLite store (`autodoc.db`) with AES-256-GCM sealed API keys (`autodoc.db.key`), you can add, switch, meter, or disable local and cloud models at runtime without restarting the daemon or interrupting lexical search.

![Provider routing architecture across config.toml, runtime RPC/TUI, local servers, cloud APIs, and lexical fallback](provider-routing-architecture.mmd)

---

## 1. Daemon Queue Configuration in `config.toml` vs. Sealed Store Providers

In AutoDoc's architecture, `~/.config/autodoc/config.toml` holds only process-level daemon and queue settings (`[server]`, `[follow]`, and `[embedding_queue]`). Provider endpoints, model names, context windows, and API keys are stored inside `autodoc.db` so they can be switched live across all attached TUI, GUI, and Neovim clients and so API secrets never sit in plaintext dotfiles.

```toml
# ~/.config/autodoc/config.toml
[server]
socket = ""      # default: $XDG_RUNTIME_DIR/autodoc.sock (mode 0600)
state_dir = ""   # default: $XDG_STATE_HOME/autodoc
data_dir = ""    # default: $XDG_DATA_HOME/autodoc (holds autodoc.db and autodoc.db.key)

[follow]
poll_interval = "2s"

[embedding_queue]
max_inflight = 1 # 1 (default) or 2 concurrent provider calls across all workspaces
```

> [!IMPORTANT]
> Placing a legacy `[embedding]` or `[[workspace]]` table in `config.toml` is intentionally rejected at startup with a descriptive error pointing to `System › Search models…` (`SPC s`) and `embedding.*` / `ranker.*` RPC verbs.

### Sealed API Keys (`autodoc.db.key`)

When you register a cloud provider (`ollama-cloud`, `openai`, or `rerank-api`) with a secret `key`, AutoDoc encrypts the credential using **AES-256-GCM** under a 256-bit master key stored beside the database at `$XDG_DATA_HOME/autodoc/autodoc.db.key` (file mode `0600`).

- If `autodoc.db.key` has permissions wider than `0600`, the daemon refuses to open keys (`-32068 rpc.provider_refused: "the store's keyslot file is readable by others: make it 0600"`).
- Subsequent `embedding.providers` or `ranker.list` calls return `"has_key": true` without ever echoing the secret back over RPC or into the UI.
- Updating a provider with an omitted `key` preserves the existing sealed key; passing `"key": ""` erases it.

---

## 2. Recommended Local Model Stack (`Ollama` + `TEI`)

For local workstations and developer servers, AutoDoc's recommended stack pairs **Ollama** for Stage 2 bi-encoder embeddings with **HuggingFace Text Embeddings Inference (TEI)** for Stage 3 cross-encoder reranking.

![GPU VRAM usage at 8k context tokens versus Top-3 retrieval recall across embedding and reranker stacks](model-accuracy-vs-vram.chart.json)

### Why Context Window (`num_ctx`) Controls GPU VRAM

Each Markdown section is embedded as an independent stateless request. However, Ollama allocates KV-cache memory according to the `num_ctx` parameter sent with the request:

- Left to a model's maximum context length (`40,960` tokens for `qwen3-embedding:4b`), Ollama reserves **12.4 GB** of VRAM.
- AutoDoc defaults every Ollama provider's context window to **`8,192` tokens** (`context = 8192`, sent as `num_ctx` on every request), reducing `qwen3-embedding:4b`'s VRAM footprint to **4.6 GB** and `qwen3-embedding:0.6b`'s footprint to **1.2 GB**.
- Because AutoDoc splits documents at headings (`section_tokens = 350`), almost all chunks are well under 1,000 tokens. If an extraordinary chunk exceeds the model's context window (HTTP `400`, `413`, or `422`), the indexer isolates the single oversized chunk, sets it aside for one hour (`failing` in `index.status`), and embeds the rest of the batch; the oversized section remains searchable via Stage 1 lexical BM25.

### Starting Local Ollama and TEI Servers

```sh
# 1. Pull recommended local embedding models in Ollama (http://127.0.0.1:11434)
ollama pull qwen3-embedding:0.6b
ollama pull qwen3-embedding:4b

# 2. Launch HuggingFace TEI with BAAI/bge-reranker-v2-m3 on port 8080 (Docker GPU or CPU)
docker run -d --name autodoc-reranker \
  --gpus all -p 127.0.0.1:8080:80 \
  -v ~/.cache/huggingface:/data \
  ghcr.io/huggingface/text-embeddings-inference:1.5 \
  --model-id BAAI/bge-reranker-v2-m3
```

---

## 3. Managing Embedding Providers via RPC and TUI (`SPC s`)

In the TUI or GUI, press `SPC s` (`System › Search models…`) to open the two-tab **Search models** dialog (**Embedding Models** and **Ranker Models**). Every action in that dialog maps directly to the `embedding.*` RPC domain (`core/embedding`).

### Adding, Probing, and Activating an Embedding Provider

Before AutoDoc saves or switches to a provider, it sends a live probe request (`"probe"`) to verify that the endpoint is reachable, the API key is accepted, and the selected model returns numeric vectors rather than chat completions (catching mistakes like selecting `gpt-oss-20b` instead of an embedding model).

```sh
# Register local Ollama qwen3-embedding:0.6b (1024 dims, 8192 token context)
autodoc --call embedding.add '[
  {
    "name": "local-qwen-0.6b",
    "kind": "ollama",
    "base_url": "http://127.0.0.1:11434",
    "model": "qwen3-embedding:0.6b",
    "context": 8192
  }
]'

# Register an OpenAI-compatible cloud provider (key is sealed in autodoc.db.key)
autodoc --call embedding.add '[
  {
    "name": "openai-small",
    "kind": "openai",
    "base_url": "https://api.openai.com/v1",
    "model": "text-embedding-3-small",
    "key": "sk-proj-..."
  }
]'

# Query available models and maximum context window reported by a provider
autodoc --call embedding.models '["local-qwen-0.6b"]'
autodoc --call embedding.model_context '["local-qwen-0.6b", {"name": "local-qwen-0.6b", "kind": "ollama", "base_url": "http://127.0.0.1:11434", "model": "qwen3-embedding:0.6b"}]'

# List all configured providers, the active provider, and any startup/runtime error
autodoc --call embedding.providers

# Activate local-qwen-0.6b across the daemon
autodoc --call embedding.use '["local-qwen-0.6b"]'

# Inspect daily request/token usage and the last 20 provider calls
autodoc --call embedding.usage '["local-qwen-0.6b", 14]'
autodoc --call embedding.log '["local-qwen-0.6b", 20]'
```

### Per-Workspace Provider Overrides and Embedding Policies

While `embedding.use` sets the daemon-wide default provider, individual workspaces can override both the provider and the scheduling policy (`Manage workspaces… › Advanced…`):

- **`embedding_policy` (`workspace.embedding_policy`):**
  - `"always"` *(default)*: Embeds new and edited sections continuously in the background.
  - `"when opened"`: Defers embedding until a client opens or searches the workspace, then continues in the background.
  - `"never"`: Disables semantic indexing for the workspace (words-only lexical search).
- **`provider` (`workspace.set_provider`):** Pins a workspace to a specific stored provider (`""` uses the daemon's active provider). The background scheduler groups up to 16 consecutive batches per model so a local Ollama server does not thrash VRAM swapping between two workspace models.

---

## 4. Configuring Cross-Encoder Rerankers (`core/rank`)

Stage 3 cross-encoder rerankers are managed through the **Ranker Models** tab in `SPC s` or via the `ranker.*` (`rank.*`) RPC methods. AutoDoc supports two external ranker kinds plus compile-time supplied rankers:

1. **`"tei"`:** HuggingFace Text Embeddings Inference `/rerank` endpoint (`http://127.0.0.1:8080`). AutoDoc queries `/info` (`ranker.models`) to discover the loaded cross-encoder model automatically.
2. **`"rerank-api"`:** Any Cohere/Voyage/Jina-compatible `/v1/rerank` HTTP endpoint taking `{"model", "query", "documents", "top_n"}` and returning `relevance_score` per candidate.

```sh
# 1. Add a local TEI cross-encoder reranker
autodoc --call ranker.add '[
  {
    "name": "tei-bge-m3",
    "kind": "tei",
    "base_url": "http://127.0.0.1:8080",
    "model": "BAAI/bge-reranker-v2-m3"
  }
]'

# 2. Activate the reranker (or pass "" to turn reranking off: rank.off)
autodoc --call ranker.use '["tei-bge-m3"]'

# 3. Tune the candidate window (10..100, default 40; use 15..20 for CPU inference)
autodoc --call ranker.window '[25]'

# 4. Inspect configured rankers, active state, window size, and daily usage metrics
autodoc --call ranker.list
autodoc --call ranker.usage '["tei-bge-m3", 7]'
autodoc --call ranker.log '["tei-bge-m3", 20]'

# 5. Turn cross-encoder reranking off without deleting the provider configuration
autodoc --call ranker.use '[""]'
```

![Configuration matrix for local and cloud embedding and reranker providers](provider-config-matrix.csv)

---

## 5. Non-Blocking Model Switching and Automatic Vector GC (`reclaim.go`)

Changing embedding models in a large knowledge base requires re-embedding every chunk because vectors from different models (for example, 768-dim `nomic-embed-text` vs. 2,560-dim `qwen3-embedding:4b`) inhabit incompatible vector spaces. AutoDoc (`core/index/reclaim.go`, ADR 1791284787) orchestrates model transitions with zero search downtime and automatic storage reclamation:

### Step-by-Step Model Switch Lifecycle

1. **Immediate Unload of Old Model:** When you invoke `embedding.use '["local-qwen4b"]'`, the daemon probes the new model, marks it as the workspace's `target` model in SQLite (`model.target = 1`), and immediately takes the previous model offline—instructing Ollama (`keep_alive: 0`) to unload the old model from GPU RAM so both models are never resident simultaneously.
2. **Unblocked Lexical + Reranked Search (`semantic: "switching"`):** While the background indexer embeds sections under the new target model fingerprint (`fp`), `search.query` never blocks or mixes incompatible vector spaces. It answers immediately using Stage 1 Lexical FTS5 (plus Stage 3 Reranking if active) and sets `Result.Semantic = "switching"` and `stages.skipped.semantic = "a new embedding model is filling"`.
3. **Atomic Cutover:** Once every chunk in the workspace has its vector under the target model, the indexer flips `model.active = 1` in a single write transaction. Any open search picker in the TUI or GUI automatically refreshes its hits with hybrid semantic ranking.
4. **Cancelling an In-Flight Switch (`embedding.cancel_switch`):** If you cancel a switch before completion, AutoDoc restores the previous model to active duty and queues the partial target vectors for immediate deletion.

### Batched Vector Garbage Collection (`core/index/reclaim.go`)

AutoDoc guarantees that each workspace retains vectors for at most its `active` model and its in-flight `target` model. Every superseded, cancelled, or removed model fingerprint is swept by `reclaim.go`:

- **Interleaved Write Batches (`reclaimBatch = 2000`):** To prevent long SQLite write locks from stalling file saves or indexing, `Store.reclaim` deletes obsolete vectors in batches of `2,000` `text_hash` rows per transaction (`core/index/reclaim.go:27-95`).
- **Pre-Batch Safety Check:** Before deleting each batch of 2,000 vectors, the transaction re-verifies that `m.Active == 0 && m.Target == 0`. If the user switched back to that model mid-reclaim, garbage collection halts immediately and preserves all remaining vectors so the indexer only has to fill the delta.
- **Page Reclamation (`IncrementalVacuum`):** After removing the last vector batch and deleting the `model` metadata row, AutoDoc runs `s.db.IncrementalVacuum(ctx)` to return freed SQLite pages to the operating system.

```sh
# Inspect stored models and byte footprints (f32_bytes, bits_bytes, key_bytes) for workspace "kb"
autodoc --call index.models '["kb"]'

# Manually purge an inactive model fingerprint or compact the entire SQLite store when stopped
autodoc --call index.purge_model '["kb", "ollama:nomic-embed-text:768"]'
autodoc --compact
```

---

[← Previous: Chapter 05: Hybrid Search Pipeline](./05-hybrid-search-pipeline.md) | [Index](./README.md) | [Next: Chapter 07: Frontmatter Schemas & Validation →](./07-frontmatter-schemas-and-validation.md)

