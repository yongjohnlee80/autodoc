# Chapter 8: Knowledge Graphs and the ADR Lifecycle

Engineering documents do not exist in isolation: an Architecture Decision Record (`ADR-0009`) supersedes an earlier design (`ADR-0001`), is amended by a follow-up (`ADR-0014`), is backed by a benchmark synthesis note, is verified by a code review, and is tracked by an active `.todo-list/` task.

AutoDoc (`core/index/graph.go`, powered by `golib/graph`) extracts every inline link, resource embed, and typed YAML frontmatter relation into a unified, incrementally maintained knowledge graph inside SQLite.

![ADR supersession, amendment, synthesis citation, and code review lifecycle graph](adr-supersession-lifecycle.mmd)

---

## 1. Unified Link Extraction: Wikilinks, Markdown Links, and Typed Frontmatter

Whenever a file is indexed or modified, AutoDoc extracts three families of directed graph edges (`LinkRow` in `core/index`):

### 1. Obsidian-Style `[[wikilinks]]` (`Kind: "wikilink"`)

AutoDoc supports the full Obsidian wikilink grammar inside Markdown prose:

- **Stem Links (`[[0203-storage]]`):** Matches the target document by filename stem anywhere in the workspace.
- **Path-Qualified Links (`[[adrs/0203-storage]]`):** Disambiguates stems that appear in multiple folders by matching the path suffix.
- **Heading and Block Anchors (`[[0203-storage#4-lease]]` or `[[0203-storage#^lease-invariant]]`):** Stores the target anchor (`Anchor: "4-lease"` or `"^lease-invariant"`) so `SPC j` jumps directly to the referenced heading or block ID.
- **Display Labels (`[[adrs/0203-storage#4-lease|Storage Lease Protocol]]`):** Renders the human-readable label while resolving the underlying target path.

### 2. Standard Markdown Links and Resource Embeds (`Kind: "markdown"` and `"embed"`)

- **Relative Markdown Links (`[Storage Lease](../adrs/0203-storage.md#lease)`):** Resolved relative to the source document's directory (`from`).
- **Companion Resource Embeds (`![Flow](0203-storage/flow.mmd)` or `![[./flow.mmd]]`):** Links the embedded `.mmd` diagram, `.chart.json` specification, or `.csv` table to the parent note so inspecting backlinks on the asset reveals every document that embeds it.

### 3. Typed YAML Frontmatter Relations

AutoDoc promotes structured relationship fields in YAML frontmatter into first-class typed graph edges whose `Kind` equals the frontmatter field name:

- **`supersedes` / `superseded_by`:** Tracks architecture decision lineage and drives search demotion.
- **`amends`:** Connects an incremental ADR amendment to the base ADR it modifies without replacing it wholesale.
- **`sources`:** Connects `synthesis` documents (benchmarks, investigations, postmortems) to the primary documents or ADRs they cite.
- **`related`:** Captures symmetric topical cross-references.
- **`adr` and `review`:** Connects `adr-rationale` docs, `review` records, and `.todo-list/` tasks (`$KB_ROOT/...` or `$PROJECT_ROOT/...` symbolic refs, ADR 1792015212) to their governing ADRs and review artifacts.

### Example: Frontmatter Lineage Across Three Linked Documents

```yaml
# adrs/0009-sqlite-wal.md
---
type: adr
number: "0009"
status: accepted
created: 2026-08-14
tags: [sqlite, storage, wal]
supersedes: [0001-legacy-store]
abstract: "Adopt a single SQLite WAL file per daemon with snapshot readers and a single writer."
---
```

```yaml
# adrs/0014-todo-journal.md
---
type: adr
number: "0014"
status: accepted
created: 2026-10-02
tags: [sqlite, todos]
amends: [0009-sqlite-wal]
sources: [synthesis/benchmark-wal.md]
abstract: "Extend the SQLite store with a journaled .todo-list change log and consumer cursors."
---
```

![Reference table of all knowledge graph edge kinds, directions, and search ranking effects](relation-kinds-reference.csv)

---

## 2. Deterministic Workspace Link Resolution Rules

When resolving a link (`graph.resolve`), AutoDoc applies Obsidian-compatible deterministic precedence rules across the workspace:

1. **Exact Workspace-Relative Path:** If the target matches a workspace path exactly (`adrs/0203-storage.md`), it resolves immediately.
2. **Filename Stem, Path Suffix, or Frontmatter `aliases`:** Otherwise, AutoDoc looks up documents whose filename stem (`0203-storage`), trailing path suffix (`adrs/0203-storage`), or YAML `aliases:` entry matches the target.
3. **Nearest-Root Tiebreaker:** If multiple files share a stem and one sits closer to the workspace root (fewer `/` path segments), the shallowest path wins.
4. **Ambiguity Preservation (`Reason: "ambiguous"`):** If two files at the same directory depth match the link, AutoDoc refuses to guess—it marks `Resolved: false` with `Reason: "ambiguous"` so `graph.unresolved` can surface the collision.
5. **Automatic Reactive Re-Resolution:** Whenever a new file is created, renamed, deleted, or updates its `aliases:`, AutoDoc automatically re-evaluates any previously unresolved or ambiguous links that could match that target.
6. **External Todo Symbolic References (`Link.External`):** When a `.todo-list/` task references a file outside the current workspace via `$KB_ROOT/...` or `$PROJECT_ROOT/...`, AutoDoc resolves the variable (`workspace.vars`) and populates `Link.External` with the absolute target path (`core/index/graph.go:27-29`), never falsely reporting it as a broken workspace link.

![Distribution of document link connectivity across a 2,000-document engineering KB](graph-connectivity-histogram.chart.json)

---

## 3. Navigating the Graph in the TUI, GUI, and Neovim (`SPC l`)

### The Interactive Relations Drawer (`SPC l`)

Pressing `SPC l` in the TUI or GUI (or `:AutodocRelations` / `<leader>ml` in Neovim) opens the **Relations drawer** docked beside the current page. Unlike a flat backlinks list, the drawer groups connections by semantic role and direction:

- **Lineage & Governance:** `Superseded by`, `Supersedes`, `Amends`, `Amended by`, `ADR`, `Review`
- **Evidence & Context:** `Sources`, `Cited by`, `Related`
- **Prose Links:** `Links` (outbound), `Backlinks` (inbound)
- **Diagnostics:** `Unresolved links` (targets naming no document) and `Supersession loops` (circular `supersedes` chains)

Inside the Relations drawer:

- Press **`Enter`** on any row to open the target document in the editor while keeping the drawer open.
- Press **`d`** to toggle **Depth 2** expansion, which reveals documents *two hops away* nested directly beneath the intermediate neighbor that reaches them.
- On the editor page itself, press **`SPC j`** to follow the link under the cursor and **`SPC b`** (or `Ctrl+O` in Normal mode, `:AutodocBack` / `<leader>mo` in Neovim) to jump back to the exact line and view you left.

---

## 4. Querying Links, Backlinks, and Neighborhoods via RPC

AI agents and scripts can traverse the knowledge graph without parsing Markdown files by calling the `graph.*` RPC domain (`core/index/graph.go`):

```sh
# 1. List all outbound links from a document (optionally filtered by edge kinds)
autodoc --call graph.links '[
  "kb",
  "adrs/0009-sqlite-wal.md",
  {"kinds": ["supersedes", "amends", "wikilink"]}
]'

# 2. List all inbound backlinks pointing to a document
autodoc --call graph.backlinks '[
  "kb",
  "adrs/0009-sqlite-wal.md"
]'

# 3. Extract the 2-hop subgraph (nodes + directed edges) around a document
#    (graph.neighborhood / graph.neighbors & graph.path traversal)
autodoc --call graph.neighborhood '[
  "kb",
  "adrs/0009-sqlite-wal.md",
  2,
  {"kinds": ["supersedes", "superseded_by", "amends", "sources", "adr"]}
]'

# 4. Test how a raw link string resolves from a given source file (saved or unsaved)
autodoc --call graph.resolve '[
  "kb",
  "synthesis/wal-benchmarks.md",
  "[[0009-sqlite-wal#concurrency]]"
]'
```

Example `graph.neighborhood` output:

```json
{
  "nodes": [
    "adrs/0001-legacy-store.md",
    "adrs/0009-sqlite-wal.md",
    "adrs/0014-todo-journal.md",
    "synthesis/benchmark-wal.md"
  ],
  "edges": [
    {"src": "adrs/0001-legacy-store.md", "dst": "adrs/0009-sqlite-wal.md", "kind": "superseded_by"},
    {"src": "adrs/0009-sqlite-wal.md", "dst": "adrs/0001-legacy-store.md", "kind": "supersedes"},
    {"src": "adrs/0014-todo-journal.md", "dst": "adrs/0009-sqlite-wal.md", "kind": "amends"},
    {"src": "synthesis/benchmark-wal.md", "dst": "adrs/0009-sqlite-wal.md", "kind": "sources"}
  ]
}
```

When `kinds` is passed in `options`, `core/index/graph.go:50-61` pushes the `IN (...)` predicate directly into the SQLite query before constructing the `golib/graph` walk—ensuring a 2-hop neighborhood filtered to `["supersedes", "amends"]` never traverses an unrelated prose `wikilink` on the first hop.

---

## 5. Superseded ADR Demotion (`demote_superseded`) and Graph Hygiene (`graph.unresolved`)

### Automatic Demotion of Superseded Decisions in Search

In long-lived engineering organizations, retired ADRs remain in the repository for historical context (`status: superseded`) and often contain the exact keywords a developer searches for. If a search returns `ADR-0001` above `ADR-0009`, an agent or engineer may accidentally implement the deprecated design.

AutoDoc prevents this with **supersession-aware ranking** (`demote_superseded: true` by default on every workspace, overridable per query via `retrieval.demote_superseded`):

1. **Bidirectional Edge Inference:** Whether `ADR-0001` declares `superseded_by: [0009-sqlite-wal]` or `ADR-0009` declares `supersedes: [0001-legacy-store]`, AutoDoc resolves the transitive successor along the supersession chain.
2. **Relative Ordering Guarantee:** Whenever hits from a superseded document and its active successor both appear in `search.query`, AutoDoc moves the superseded document's hits below the successor's hits and sets `"superseded_by": "adrs/0009-sqlite-wal.md"` on each demoted hit.
3. **Cycle Safety:** If two documents accidentally declare a circular supersession loop (`A supersedes B` and `B supersedes A`), AutoDoc detects the cycle, surfaces it under **Supersession loops** in the `SPC l` Relations drawer, and leaves their search order untouched.

### Auditing Broken and Ambiguous Links (`graph.unresolved`)

To keep a knowledge base free of dangling references after file renames or reorganizations, run `graph.unresolved`:

```sh
# Audit all broken or ambiguous links across the workspace
autodoc --call graph.unresolved '["kb"]'

# Audit only broken frontmatter relation links (supersedes, sources, adr)
autodoc --call graph.unresolved '[
  "kb",
  {"kinds": ["supersedes", "superseded_by", "amends", "sources", "adr"]}
]'
```

Each entry reports `{src, raw, reason}` where `reason` is either `"missing"` (no indexed file matches the target) or `"ambiguous"` (multiple files at equal depth match the short name, requiring a folder prefix to disambiguate).
