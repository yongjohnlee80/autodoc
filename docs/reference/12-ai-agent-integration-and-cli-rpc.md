---
title: "Chapter 12: AI Agent Integration, Adaptive Retrieval, and CLI RPC"
type: reference
status: active
created: 2026-10-17
tags: [autodoc, reference-book, ai-agents, cli, rpc, contracts, kb-operations, benchmarks]
abstract: "How to wire external AI coding agents (Claude Code, Gemini CLI / Antigravity, OpenAI Codex, Aider) to AutoDoc using autodoc --call and self-describing autodoc --contracts, implement the KB_OPERATIONS.md adaptive retrieval ladder, and achieve the token and latency reductions measured in the 96-session benchmark study."
---

# Chapter 12: AI Agent Integration, Adaptive Retrieval, and CLI RPC

When an AI coding agent—such as Claude Code, Gemini CLI / Antigravity, OpenAI Codex, or Aider—is dropped into a multi-thousand-document Knowledge Base or software repository, naive `grep` and full-file reads quickly exhaust its context window and inflate latency. Retired logs in `archive/`, raw transcripts in `raw/`, and superseded ADRs often share the same keywords as active architectural rules, causing unweighted keyword search to surface stale guidance first.

AutoDoc gives every CLI coding agent direct, zero-configuration access to its hybrid search engine, frontmatter validator, link graph, and `.todo-list/` task store through two built-in CLI flags: `autodoc --call <verb> '<json>'` and `autodoc --contracts`. Furthermore, AutoDoc ships a versioned operating manual (`KB_OPERATIONS.md`) that teaches agents an **adaptive retrieval ladder**: probing the daemon's active pipeline stages once per session and reading exact section line spans (`[line_start, line_end]`) instead of entire files.

---

## 1. Self-Describing RPC Contracts (`autodoc --contracts`)

Under the AutoDoc Module Convention (ADR 1791719881, `core/module`), every RPC verb in the daemon is registered from a declarative `module.Contract` struct (`rpc/contracts.go`, `core/todo/module.go`). A contract specifies the verb's name, the protocol version it arrived in (`Since`), its `Scope` (`Global`, `Workspace`, `LocalPeer`), its `Audience` (`Agent`, `User`, `Internal`), its ordered positional `Params`, its `Result` shape, and the numeric `PublicError` codes it may return.

Because `Module.Verbs()` and `Module.Errors()` are pure data functions that perform no I/O, an agent or tool can inspect the complete RPC catalog offline—even when no daemon is running—using `autodoc --contracts`:

```sh
# Print the full catalog (protocol version, all verbs, and all error codes) as JSON
autodoc --contracts

# Filter to a single domain or verb and render Markdown reference tables
autodoc --contracts 'todos.*' --format md
autodoc --contracts 'search.query' --format md
```

Inside the repository, `go generate ./rpc` runs `internal/contractsgen` over this exact catalog to generate the verb and error tables in `AGENTS.md` and `autodoc.nvim`'s `lua/autodoc/contracts.lua`. A golden contract test (`TestContractsAreGolden` and `TestAgentsMDIsGenerated`) guarantees that documentation, CLI help, and wire dispatch never drift apart.

---

## 2. Wiring CLI Coding Agents to `autodoc --call`

**From a shell, or an AI agent.** `autodoc --call <verb> '<JSON array of parameters>'` calls any verb of the daemon (starting it when nothing answers) and prints the result as JSON:

```sh
autodoc --call workspace.list
autodoc --call search.query '["kb", "storage decision", {"paths": ["adrs"], "limit": 10}]'
```

[AGENTS.md](../../AGENTS.md) tells an AI agent how to search with it: the verbs, the query syntax, the options (`stages`, `paths`, `tags`, `facets`, `limit`) and the errors.

A typical agent session:

```sh
# 1. Discover available workspaces and confirm "kb" is in state "ready"
autodoc --call workspace.list '[]'

# 2. Run a 3-stage hybrid search scoped to active ADRs and conventions
autodoc --call search.query '["kb", "merge a stacked PR with --delete-branch", {"paths": ["conventions", "adrs"], "limit": 3}]'

# 3. Validate a draft Markdown document against _schema/frontmatter.yaml
autodoc --call doc.validate '["kb", "adrs/1792200100-my-decision.md", "---\ntype: adr\n..."]'
```

### Daemon Autostart, Handshake, and Structured Error Recovery

`autodoc --call` handles connection lifecycle automatically so agents never need custom socket-dialing code:

- **Endpoint Discovery & Autostart**: `--call` locates the active Unix domain socket (`$XDG_RUNTIME_DIR/autodoc.sock` by default, or via the store's lease-info file) and automatically spawns `autodoc --serve` in the background if no daemon is listening.
- **Protocol Negotiation (`sys.hello`)**: `--call` identifies itself as `"autodoc-cli"` at the binary's protocol version (so mutating calls are attributed to `"autodoc-cli"` in `sys.events`). If the running daemon is an older binary within `[MinProtocol, Protocol]`, `--call` transparently reconnects at the daemon's `server_protocol`.
- **Actionable Error Codes**: On failure, `autodoc --call` exits with status `1` and prints `{"error": {"code": -32071, "message": "the task changed since it was read"}}` on `stderr`. Every code in `autodoc --contracts` includes a deterministic `next` instruction telling the agent how to recover (for example, `-32062 rpc.conflict` -> *"doc.read, then merge or retry"*; `-32069 rpc.switching` -> *"a new model is filling: search with lexical among the stages until it is ready"*).

### Configuring Claude Code, Gemini CLI / Antigravity, Codex, and Aider

**An agent acts as you.** One AutoDoc starts gets `AUTODOC_SOCKET` and the `autodoc` CLI on its PATH, so it can call every verb of the daemon, `file.write` anywhere on your disk among them, and it runs commands of its own. AutoDoc's Lua grants do not apply to it. Start only an agent you would let act for you.

When AutoDoc spawns an embedded agent tab (`tui/agentinstances.go`), it automatically sets `$AUTODOC_WORKSPACE` and writes a workspace agent guide pointing to `AGENTS.md` and `KB_OPERATIONS.md`. For external agent sessions launched in standalone terminals or Neovim (`auto-agents`), add two rules to your project's `AGENTS.md`, `CLAUDE.md`, or `GEMINI.md`:

1. Export `AUTODOC_WORKSPACE="<workspace-name>"` (or resolve it via `autodoc --call workspace.list '[]'`).
2. Instruct the agent to read `KB_OPERATIONS.md` on startup and check its `revision:` frontmatter field.

---

## 3. The `KB_OPERATIONS.md` Adaptive Retrieval Ladder

Not every machine runs the same local models: a workstation may run Ollama with `snowflake-arctic-embed2` and a `bge-reranker-v2-m3` cross-encoder, while a lightweight laptop or CI container may run words-only (`lexical`) indexing or temporarily switch models.

Rather than hardcoding one search strategy, `KB_OPERATIONS.md` teaches agents to **let the first `search.query` response dictate the retrieval mode for the session** by inspecting `stages.performed` and `stages.skipped`:

![Adaptive AI Agent Retrieval Ladder and Validation Protocol](agent-retrieval-ladder.mmd)

The binary carries the manual, so an install without the Neovim plugin (Homebrew, mise, `go install`) can read it with no daemon: `autodoc --kb-operations` prints it as a KB's scaffold writes it, stamped with the build's version. `autodoc --tutorial` lists the short tutorial's pages, and `autodoc --tutorial 6` prints page 6, on the search models.

### Step 1: Known Target vs. Topic Lookup

- **Known Target? Go Straight to It.** If the agent already holds an exact file path, a task ID, an ADR number from a task's `adr:` field, or a literal error string, it reads the file directly or runs a targeted `rg`. Searching for a file you can already name wastes a round-trip.
- **Topic Lookup? Probe with `search.query` (`limit: 3`).** Pass a natural-language phrase of 4 to 8 words (e.g., `"conditional write version conflict vfs"`) and scope `paths` to live folders (`["adrs", "conventions", "synthesis"]`), excluding `archive/` and `raw/` unless researching historical incidents.

### Step 2: Adapt to `stages.performed` vs. `stages.skipped`

Every `search.query` response includes a `stages` diagnostic block:

```json
{
  "mode_used": "hybrid",
  "semantic": "ready",
  "rank": {"state": "ready", "model": "BAAI/bge-reranker-v2-m3", "error": ""},
  "stages": {
    "requested": ["lexical", "semantic", "rerank"],
    "performed": ["lexical", "semantic", "rerank"],
    "skipped": {}
  }
}
```

1. **`semantic` and `rerank` both performed (Best Case)**: Keep `limit: 3`. With cross-encoder reranking active, the answer document sits in the top 3 hits 94%–100% of the time, and `limit: 3` consumes ~60% fewer tokens than `limit: 10`.
2. **`semantic` performed, `rerank` skipped**: Increase `limit` to `10`. Without the reranker, RRF fusion places the answer in the top 3 on 72% of queries and in the top 10 on 91%.
3. **`semantic` skipped (`lexical` only)**: Switch to the **Words-Only Ladder**:
   - Never pass a full conversational sentence to FTS5 lexical search (which requires every word to appear in the same section and fails on 17 of 18 conversational queries).
   - Instead, pass **2 to 3 distinctive keywords** or a starred prefix (`"--delete-branch stacked*"`).
   - Combine lexical search with `index.documents` (filtering by `facets`, `tags`, `paths`, or `sort: "updated"`), `doc.outline` (for heading hierarchy), and `graph.neighborhood` / `graph.links` / `graph.backlinks` (to follow `supersedes`, `superseded_by`, `amends`, and `adr` relations).
   - If one retry with a dropped word or prefix still misses, fall back to scoped `rg -il --glob '!archive/**' --glob '!raw/**'`.

### Step 3: Read `[line_start, line_end]` Spans, Not Whole Files

Each hit in `search.query` returns exact 1-based section boundaries: `line_start`, `line_end`, `byte_start`, `byte_end`, and the heading `breadcrumb`. Agents should read **only `[line_start, line_end]`** using their line-ranged Read tool (or `doc.read` / section slices) and stop as soon as the question is answered. Read a document end-to-end only when acting on an entire convention, ADR, or review.

---

## 4. Empirical Findings from the 96-Session Benchmark Study

The rules in `KB_OPERATIONS.md` are grounded in controlled measurements recorded in `docs/benchmarks/kb-retrieval.md` across two production Knowledge Bases:

- **KB A**: 2,200 documents, 32 real engineering lookups (62 accepted answer passages across title, section, paraphrase, and fact questions), evaluated across **96 independent agent sessions** (32 questions × 3 retrieval methods) using Ollama `snowflake-arctic-embed2` (1024-d) and a reranker.
- **KB B**: 18 lookups (half paraphrased) evaluated with `bge` embeddings and `bge-reranker-v2-m3`, plus 30 days of live agent transcript telemetry (~29 KB searches and 18 reads per day).

![AI Agent KB Retrieval Cost: Grep vs AutoDoc Lexical vs AutoDoc 3-Stage Hybrid](agent-token-and-time-savings.chart.json)

### Key Takeaways from the Benchmark Data

- **Reranking Locks In Top-3 Accuracy**: On KB A, `semantic + reranker` placed the answer document in the top 3 for **94%** of queries (matching top-10 recall of 94%) and the exact answer section in the top 3 for **81%**. On KB B, scoped `paths` with all stages placed the answer in the top 3 on **18 of 18 (100%)** lookups and #1 on **15 of 18 (83%)**. By contrast, unscoped `grep` over the whole tree ranked the answer #1 only **3%** of the time (and **22%** even after excluding `archive/` and `raw/`).
- **Massive Token Reduction via Section-Range Reads**: Across 30 days of agent telemetry, reading a whole KB file pulled a median of **2,600 tokens** into context, whereas reading a `search.query` hit's `[line_start, line_end]` span pulled only **~220 tokens**—an **11.8× reduction per read**. Across full multi-turn agent sessions on KB A, AutoDoc search-first cut total KB context tokens pulled by **23%** compared to pre-v2 grep (mean 7,140 vs. 9,238 tokens; p90 dropped from 13,439 to 9,067 tokens) and reduced median per-lookup read volume from **1,924 tokens** (grep + full-file read) to **1,120 tokens** (AutoDoc lexical) and **744 tokens** (AutoDoc 3-stage hybrid).
- **43% Faster End-to-End Wall Time**: On KB A, agents using AutoDoc search-first completed lookups in a mean wall time of **8.8 seconds** (2.47 tool calls), compared to **15.4 seconds** (3.09 tool calls) with grep—cutting end-to-end latency by **43%** while eliminating context pollution from retired `archive/index.md` and `log.md` files.

![Reference of Primary RPC Verbs Used by AI Coding Agents](agent-rpc-verbs-reference.csv)

---

## Summary

By combining self-describing contracts (`autodoc --contracts`), zero-config CLI invocation (`autodoc --call`), and the empirical retrieval ladder in `KB_OPERATIONS.md`, AI coding agents retrieve exact architectural context in seconds at a fraction of the token budget. In Chapter 13, we examine how AutoDoc guarantees that when multiple agents, Neovim buffers, and GUI editors write back to the workspace simultaneously, no edit is ever silently lost or corrupted.

---

[← Previous: Chapter 11: Terminal Tabs & Agent Handoff](./11-terminal-tabs-and-agent-handoff.md) | [Index](./README.md) | [Next: Chapter 13: Concurrency & Safe Multi-Agent Writes →](./13-concurrency-and-safe-multi-agent-writes.md)

