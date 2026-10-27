# Chapter 6: Configuring Embeddings and Rerankers

AutoDoc separates its always-on lexical index from its optional neural inference tiers: dense bi-encoder embeddings (`core/embedding`) and cross-encoder rerankers (`core/rank`). Because both tiers are managed dynamically by the daemon and persisted in the SQLite store (`autodoc.db`) with AES-256-GCM sealed API keys (`autodoc.db.key`), you can add, switch, meter, or disable local and cloud models at runtime without restarting the daemon or interrupting lexical search.

Semantic search finds a file by what it means, not only by the words it shares with the query. A search for "why did we pick SQLite" also finds the file that says "we chose an embedded database", and "login bug" finds "authentication fails". Exact words still count: the two are fused, and a file that matches both the words and the meaning ranks highest.

**How it works.** An embedding model turns each section of a file into a vector, a list of numbers that places the text by meaning. A query is turned into a vector the same way, and the sections whose vectors are closest are the matches.

![Provider routing architecture across config.toml, runtime RPC/TUI, local servers, cloud APIs, and lexical fallback](provider-routing-architecture.mmd)

---

## 1. Daemon Queue Configuration in `config.toml` vs. Sealed Store Providers

In AutoDoc's architecture, `~/.config/autodoc/config.toml` holds only process-level daemon and queue settings (`[server]`, `[follow]`, and `[embedding_queue]`). Provider endpoints, model names, context windows, and API keys are stored inside `autodoc.db` so they can be switched live across all attached TUI, GUI, and Neovim clients and so API secrets never sit in plaintext dotfiles. The preferences and the search models are kept in the daemon's store, so they are the same whichever workspace is open.

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

When you register a cloud provider (`ollama-cloud`, `openai`, or `rerank-api`) with a secret `key`, AutoDoc encrypts the credential using **AES-256-GCM** under a 256-bit master key stored beside the database at `$XDG_DATA_HOME/autodoc/autodoc.db.key` (file mode `0600`), made the first time a key is kept.

- A copied store without that file opens no key.
- If `autodoc.db.key` has permissions wider than `0600`, the daemon refuses to open keys (`-32068 rpc.provider_refused: "the store's keyslot file is readable by others: make it 0600"`).
- Subsequent `embedding.providers` or `ranker.list` calls return `"has_key": true` without ever echoing the secret back over RPC or into the UI.
- Updating a provider with an omitted `key` preserves the existing sealed key; passing `"key": ""` erases it.

---

## 2. Recommended Local Model Stack (`Ollama` + `TEI`)

For local workstations and developer servers, AutoDoc's recommended stack pairs **Ollama** for Stage 2 bi-encoder embeddings with **HuggingFace Text Embeddings Inference (TEI)** for Stage 3 cross-encoder reranking.

### Setting Up the Embedding Model from AutoDoc (`Set up…`)

Once Ollama is installed and running, AutoDoc sets up the embedding side itself. Open **Search models › Embedding Models › Set up…**. The command palette also has it as *set up semantic search*, and the search picker offers it while semantic search is off. The dialog works in five steps:

1. **Check** asks the Ollama at `http://localhost:11434`; edit the URL to ask another. AutoDoc reads what this machine has from the system, without a network: the GPU and its memory, the RAM, the free disk, and Ollama's version.
2. It lists the models AutoDoc has measured. Each shows where it fits here (on the GPU, in memory, too large, or can't tell), its download size, and whether Ollama already has it. The recommended one is picked: the best fit on a GPU, or the lightest multilingual model on a CPU.
3. It shows every workspace with a tick, what it does now, what **Use** will do to it, and three estimates: its texts, the room their vectors take, and the time to embed them. The time appears once the model is installed, because it is measured on a sample of the workspace's own texts. A workspace with its own provider keeps it unless you tick it. An unticked workspace that would embed with the daemon's provider is turned off, and its row says so.
4. **Download** pulls the model into Ollama. The daemon owns the download, so closing the dialog doesn't stop it. **Stop download** cancels it: Ollama stops unless another client is pulling the same model, and a later download resumes where it stopped.
5. **Use** applies the plan in order:
   - it turns off the unticked workspaces;
   - it adds the provider, or reuses an equal one;
   - it makes that provider the one in use;
   - it sets each ticked workspace's provider and policy.

   A step that fails stops the plan there, and the dialog names that step. **Retry** runs the whole plan again; steps that already finished do nothing the second time.

The dialog is built on six verbs, which agents can call too:

```sh
autodoc --call host.probe                    # the GPU, RAM, free disk and Ollama's version ("" when none answers)
autodoc --call embedding.catalog             # the measured models: where each fits, the one recommended, whether it is installed
autodoc --call embedding.estimate '["qwen3-embedding:0.6b", ["kb"]]'   # per workspace: texts, bytes, and seconds once installed
autodoc --call embedding.pull '["qwen3-embedding:0.6b"]'               # starts the download; answers its job
autodoc --call embedding.pull_status '["<job>"]'                       # running, done, failed or cancelled, with bytes, rate and time left
autodoc --call embedding.pull_cancel '["<job>"]'
```

`host.probe`, `embedding.catalog`, `embedding.estimate` and `embedding.pull` take the Ollama's URL as an optional last parameter. Before the model is installed, `embedding.estimate` is provisional (`"basis": "provisional"`): it counts every text and gives no time. Only one pull runs at a time. A second one is refused as a conflict, and a status or cancel for a job that is no longer the current one is not found.

### Which Embedding Model

An embedding model, not a chat model. A chat model such as `gpt-oss-20b` produces text, not vectors: it appears in Ollama's model list, but AutoDoc embeds a probe text before it switches, and a model that cannot embed is refused, the one in use staying. For a local provider, pull one of these:

| model | input limit | |
| --- | --- | --- |
| `nomic-embed-text` | 8,192 tokens | small and fast; a good default |
| `mxbai-embed-large` | 512 tokens | stronger, on shorter sections |
| `snowflake-arctic-embed2` | 8,192 tokens | multilingual |
| `qwen3-embedding:4b` | 40,960 tokens | strong; 2,560 dimensions, about 3× snowflake's time |

They are small beside a chat model and run alongside one.

### Why Context Window (`num_ctx`) Controls GPU VRAM

![GPU VRAM usage at 8k context tokens versus Top-3 retrieval recall across embedding and reranker stacks](model-accuracy-vs-vram.chart.json)

Each Markdown section is embedded as an independent stateless request, with no conversation and no memory between requests, so nothing builds up and nothing needs flushing. What matters is the length of one section against the model's input limit (8,192 tokens for `nomic-embed-text`, 512 for `mxbai-embed-large`). Ollama also allocates KV-cache memory according to the `num_ctx` parameter sent with the request:

- Left to the server's own default (`OLLAMA_CONTEXT_LENGTH`, or the model's full context: `40,960` tokens for `qwen3-embedding:4b`), Ollama reserves **12.4 GB** of VRAM.
- AutoDoc defaults every Ollama provider's context window to **`8,192` tokens** unless you change it in the provider's form (`context = 8192`, sent as `num_ctx` on every request), reducing `qwen3-embedding:4b`'s VRAM footprint to **4.6 GB** and `qwen3-embedding:0.6b`'s footprint to **1.2 GB**. A larger window admits longer sections, at that cost.
- Because AutoDoc splits documents at headings (`section_tokens = 350`), almost all chunks are well under 1,000 tokens. A text longer than the model's context is refused (HTTP 400, 413 or 422). Ollama refuses it too, even when asked to truncate. The indexer narrows the batch to the one text refused, sets it aside for an hour, and embeds the rest. The status shows it as refused (`embeddings.refused` and `embeddings.refused_texts` in `index.status`), and the section is still found by its words (Stage 1 lexical BM25).

### Starting Local Ollama and TEI Servers

To pull the embedding models into Ollama (`http://127.0.0.1:11434`) by hand instead of with **Set up…**:

```sh
ollama pull qwen3-embedding:0.6b
ollama pull qwen3-embedding:4b
```

Start the TEI reranker with the script AutoDoc ships, [`shell/tei/tei-rerank.sh`](../../shell/tei/README.md). It runs [`compose/tei-rerank/docker-compose.yaml`](../../compose/tei-rerank/docker-compose.yaml) on an NVIDIA GPU when it finds one, and on the CPU otherwise. It picks the image tag for your GPU, waits until TEI answers, and serves it on **`http://127.0.0.1:18080`** (`TEI_PORT` changes the port):

```sh
shell/tei/tei-rerank.sh up             # GPU if one is found, else CPU; BAAI/bge-reranker-v2-m3
shell/tei/tei-rerank.sh status         # what runs, and the model it serves
shell/tei/tei-rerank.sh switch base    # another reranker (`models` lists them)
shell/tei/tei-rerank.sh down
```

The GPU needs the NVIDIA Container Toolkit; the script's [README](../../shell/tei/README.md) lists what else it needs, and how to measure a model's accuracy on your own documents.

> [!WARNING]
> **TEI's GPU images are built for one compute capability each.** The bare version tag (`:1.9`) is the Ampere 8.0 (A100) build only. On any other GPU, the container stops at start with `Runtime compute cap N is not compatible with compile time compute cap 80`. `tei-rerank.sh` reads your GPU's capability (`nvidia-smi --query-gpu=compute_cap`) and picks the tag. If you run TEI yourself, pick it the same way:
>
> | compute capability | GPUs | tag |
> |---|---|---|
> | 12.0 | RTX 50 series (Blackwell) | `120-1.9` |
> | 12.1 | DGX Spark (GB10) | `121-1.9` |
> | 10.0 | B200 (Blackwell) | `100-1.9` |
> | 9.0 | H100 (Hopper) | `hopper-1.9` |
> | 8.9 | RTX 40 series (Ada) | `89-1.9` |
> | 8.6 | RTX 30 series, A40 (Ampere) | `86-1.9` |
> | 8.0 | A100 (Ampere) | `1.9` |
> | 7.5 | RTX 20 series, T4 (Turing) | `turing-1.9` |
> | no NVIDIA GPU | | `cpu-1.9` |
>
> To check that a tag exists, ask the registry for that tag's manifest (`docker manifest inspect ghcr.io/huggingface/text-embeddings-inference:120-1.9`). Don't search the list of tags: the registry returns it in pages, alphabetically, so a page without the tag doesn't prove it's missing.

#### Checking the ranker through AutoDoc

Check a ranker through AutoDoc, never by calling TEI by hand. `search.query`'s `rank` block names the model that ranked the search, with its `state` and any `error`, and `ranker.log` lists each call with its `texts`, `millis` and `outcome`. A raw request can mislead: TEI's `/info` says `max_client_batch_size` (32 by default), and a direct `POST /rerank` of more texts fails with HTTP 422. That doesn't mean the ranker window of 40 is too large. AutoDoc reads that limit from `/info` and sends the window in batches no larger than it, so `ranker.log` shows `texts: 40` with outcome `ok`.

---

## 3. Managing Embedding Providers via RPC and TUI (`SPC s`)

In the TUI or GUI, press `SPC s` (`System › Search models…`) to open the two-tab **Search models** dialog. **Embedding Models** has the embedding providers on the left, and the one under the cursor's usage by day and latest calls on the right. **Ranker Models** has the rankers the same way, the one in use and how many candidates it ranks (`Window…`). Add…, Edit…, Use and Remove… act on the tab that is open. Every action in the Embedding Models tab maps directly to the `embedding.*` RPC domain (`core/embedding`).

### Provider Kinds

Semantic search is off until a provider is chosen in the TUI's Search models (`System › Search models…`). A provider is one of three kinds, with the model it embeds with:

- **Ollama (local):** a server on this machine or the network, `http://localhost:11434` by default. It takes no key.
- **Ollama Cloud:** `https://ollama.com`, with the account's API key, which it requires. Its models are those the cloud serves: many are chat models (GLM, gpt-oss), which cannot embed.
- **OpenAI-compatible:** any endpoint that serves `/v1/embeddings`, with a key if it takes one.

The store keeps any number of them, and one is in use; switching is a choice in a list, with no restart. The model list is the provider's own (Ollama's models, or the endpoint's `/v1/models`).

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

**Each provider keeps its usage and a log:** requests, texts, the tokens it counted, failures and usage-limit refusals by day, and its last 200 calls. A provider at its limit (HTTP 429) or refusing its key (401, 403) is named as such, so you can switch to another.

### Per-Workspace Provider Overrides and Embedding Policies

While `embedding.use` sets the daemon-wide default provider, individual workspaces can override both the provider and the scheduling policy (`Manage workspaces… › Advanced…`):

- **`embedding_policy` (`workspace.embedding_policy`):**
  - `"always"` *(default)*: Embeds new and edited sections continuously in the background.
  - `"when opened"`: Defers embedding until a client first opens the workspace, then continues in the background.
  - `"never"`: Disables semantic indexing for the workspace (words-only lexical search).
- **`provider` (`workspace.set_provider`):** Pins a workspace to a specific stored provider (`""` uses the daemon's active provider).

A workspace may embed with its own provider (`Manage workspaces… › Advanced…`): one of the stored providers instead of the daemon's. Choosing it sets it up first, and only that workspace's vectors fill again; a switch of the daemon's provider leaves it as it is, and deleting the provider returns it to the daemon's. Every provider shares the daemon's limit on requests in flight (`[embedding_queue] max_inflight`), and the queue keeps consecutive background batches on one model (up to 16 in a row) so a local server is not made to swap models every batch. A workspace whose provider cannot be set up searches by words and says why; it is never given another model.

**Embedding is scheduled across the daemon**: the open TUI workspace has first turn, then recently searched workspaces, while background work gets regular turns. The progress line says which workspace a pending one waits for, or that its setting paused embedding.

---

## 4. Configuring Cross-Encoder Rerankers (`core/rank`)

Stage 3 cross-encoder rerankers are managed through the **Ranker Models** tab in `SPC s` or via the `ranker.*` (`rank.*`) RPC methods. AutoDoc supports two external ranker kinds plus compile-time supplied rankers:

1. **`"tei"`:** HuggingFace Text Embeddings Inference `/rerank` endpoint (`http://127.0.0.1:18080`, as `tei-rerank.sh` serves it). AutoDoc queries `/info` (`ranker.models`) to discover the loaded cross-encoder model automatically.
2. **`"rerank-api"`:** Any Cohere/Voyage/Jina-compatible `/v1/rerank` HTTP endpoint taking `{"model", "query", "documents", "top_n"}` and returning `relevance_score` per candidate.

```sh
# 1. Add a local TEI cross-encoder reranker
autodoc --call ranker.add '[
  {
    "name": "tei-bge-m3",
    "kind": "tei",
    "base_url": "http://127.0.0.1:18080",
    "model": "BAAI/bge-reranker-v2-m3"
  }
]'

# 2. Activate the reranker (or pass "" to turn reranking off: rank.off)
autodoc --call ranker.use '["tei-bge-m3"]'

# 3. Tune the candidate window (10..100, default 40; use 10..20 for CPU inference)
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

### Vectors Are Made Once, and Kept

- **A file is parsed only when its file changes,** never at search time. On a restart, an unchanged file is skipped by its version (size, modification time, inode).
- **A vector lives as long as a section has its text.** When an edit or a removal takes the last section with a text, that text's vectors go in the same write. The daemon sweeps what is left over at each start, in small batches while it serves.
- **A search reads what is stored.** The only model call it makes is for the query itself.

### Step-by-Step Model Switch Lifecycle

1. **Immediate Unload of Old Model:** When you invoke `embedding.use '["local-qwen4b"]'`, the daemon probes the new model, marks it as the workspace's `target` model in SQLite (`model.target = 1`), and immediately takes the previous model offline—instructing Ollama (`keep_alive: 0`) to unload the old model from GPU RAM so both models are never resident simultaneously.
2. **Unblocked Lexical + Reranked Search (`semantic: "switching"`):** While the background indexer embeds sections under the new target model fingerprint (`fp`), `search.query` never blocks or mixes incompatible vector spaces. It answers immediately using Stage 1 Lexical FTS5 (plus Stage 3 Reranking if active) and sets `Result.Semantic = "switching"` and `stages.skipped.semantic = "a new embedding model is filling"`.
3. **Atomic Switch:** Once every chunk in the workspace has its vector under the target model, the indexer flips `model.active = 1` in a single write transaction. Any open search picker in the TUI or GUI automatically refreshes its hits with hybrid semantic ranking.
4. **Cancelling an In-Flight Switch (`embedding.cancel_switch`):** If you cancel a switch before completion, AutoDoc restores the previous model to active duty and queues the partial target vectors for immediate deletion.

### Batched Vector Garbage Collection (`core/index/reclaim.go`)

AutoDoc guarantees that each workspace retains vectors for at most its `active` model and its in-flight `target` model. Every superseded, cancelled, or removed model fingerprint is swept by `reclaim.go`:

- **A workspace keeps one model's vectors** (two while a switch fills): the old model's are deleted once the new one answers, a cancelled switch's partial vectors with the cancel, and removing the provider in use deletes its model's, unless another provider serves that model. Switching back embeds again. Unused vectors slow the scans of the ones kept, so the store does not carry them.

- **Interleaved Write Batches (`reclaimBatch = 2000`):** To prevent long SQLite write locks from stalling file saves or indexing, `Store.reclaim` deletes obsolete vectors in batches of `2,000` `text_hash` rows per transaction (`core/index/reclaim.go:27-95`).
- **Pre-Batch Safety Check:** Before deleting each batch of 2,000 vectors, the transaction re-verifies that `m.Active == 0 && m.Target == 0`. If the user switched back to that model mid-reclaim, garbage collection halts immediately and preserves all remaining vectors so the indexer only has to fill the delta.
- **Page Reclamation (`IncrementalVacuum`):** After removing the last vector batch and deleting the `model` metadata row, AutoDoc runs `s.db.IncrementalVacuum(ctx)` to return freed SQLite pages to the operating system.
- **The store gives the room back.** After the upgrade that brought this, the daemon compacts the store once, when there is free space for it: about twice the store. `autodoc --compact` does it again with the daemon stopped.

```sh
# Inspect stored models and byte footprints (f32_bytes, bits_bytes, key_bytes) for workspace "kb"
autodoc --call index.models '["kb"]'

# Manually purge an inactive model fingerprint or compact the entire SQLite store when stopped
autodoc --call index.purge_model '["kb", "ollama:nomic-embed-text:768"]'
autodoc --compact
```

---

[← Previous: Chapter 05: Hybrid Search Pipeline](./05-hybrid-search-pipeline.md) | [Index](./README.md) | [Next: Chapter 07: Frontmatter Schemas & Validation →](./07-frontmatter-schemas-and-validation.md)

