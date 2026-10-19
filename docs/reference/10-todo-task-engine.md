---
title: "Chapter 10: The Built-In Todo Task Engine"
type: reference
status: active
created: 2026-10-17
tags: [autodoc, reference-book, todos, sqlitejournal, rpc, task-engine]
abstract: "Deep dive into AutoDoc's built-in .todo-list/ task engine (core/todo): on-disk bucket hierarchy, schema-validated YAML frontmatter, portable symbolic path rewriting, the 3-stage crash-recoverable SQLite journal (sqlitejournal), the interactive Todos Panel (SPC x), and the todos.* RPC contract."
---

# Chapter 10: The Built-In Todo Task Engine

Engineering teams and AI coding agents work best when tasks live alongside the documents, architecture decision records (ADRs), and code reviews they govern. Historically, keeping task state synchronized across terminal editors, GUI viewers, and background agent processes suffered from split-brain drift: each tool re-implemented YAML serialization, timestamp rules, and directory transitions with subtle incompatibilities.

AutoDoc eliminates this drift by making its daemon (`core/todo`) the single authoritative owner of every `.todo-list/` store (ADR 1791532020). Whether a task is created from the interactive Todos Panel (`SPC x`), updated via the CLI (`autodoc --call todos.update`), scripted from Lua (`autodoc.todos.*`), or mutated through Neovim's `auto-finder` and `auto-agents` mailbox verbs, every operation flows through one pure-Go engine over `golib/vfs` and one crash-recoverable SQLite change log (`core/todo/sqlitejournal`).

---

## 1. On-Disk `.todo-list/` Layout and Bucket Hierarchy

A todo store is a plain directory of Markdown files with YAML frontmatter, located by default at `<workspace-root>/.todo-list/` (or at a custom directory registered via `todos.register` and `todos.set_dir`). Because tasks are ordinary `.md` files on disk, they can be versioned in Git, inspected with standard tools, and indexed by AutoDoc's search and link graph alongside the rest of the workspace.

Inside `.todo-list/`, a task's physical directory placement corresponds directly to its lifecycle `status`:

- `open/` — Newly created or queued tasks awaiting active work (`status: open`).
- `in-progress/` — Tasks actively being implemented by a human or assigned agent (`status: in-progress`).
- `deferred/` — Tasks intentionally paused or postponed (`status: deferred`).
- `completed/` — Finished tasks retained in the active working set for up to 28 days (`status: completed`).
- `archived/YYYY/MM/` — Completed tasks older than 28 days (or explicitly archived), partitioned by year and month of completion/archival so active listing remains instantaneous even as historical archives grow to thousands of files.
- `.autodoc-owner` — The durable cutover marker written once the daemon claims write ownership of the store (`todos.claim`).
- `.trash/` — Staging directory used during the 3-phase crash-safe deletion protocol (`todos.remove` and `todos.remove_malformed`).

### Stat-Keyed Read Caching and On-Demand Archives

To keep the UI and CLI responsive on stores with hundreds of active tasks and thousands of archived items, `core/todo` maintains an in-memory listing cache keyed by `(path, size, mtime)`. Calling `todos.list` without a `buckets` filter stats only the four active directories (`open/`, `in-progress/`, `deferred/`, `completed/`) and re-decodes only files whose stat tuple changed.

Archived tasks are never pre-loaded or cached in memory. Passing `{"buckets": ["archived"]}` returns only the partition summary (`[{month: "2026/10", count: 42}, ...]`) by reading directory entries, and passing `{"buckets": ["archived/2026/10"]}` decodes that single month on demand.

---

## 2. Task Schema: Managed vs. Hand-Editable Frontmatter

Every task file is validated against an embedded schema in `core/schema` format (`core/todo/formats/v1.yaml`), selected by `version: 1` in the YAML header. A central design principle of `core/todo` is **"reading never writes"**: when the daemon reads a valid task file, it tolerates missing header comments or formatting nuances without rewriting the file behind the user's back. When the daemon writes a task, it renders deterministic, byte-compatible YAML in a canonical field order (`FRONTMATTER_ORDER`), followed by a `# <title>` H1 heading and the Markdown body.

```yaml
---
# Managed by AutoDoc — do not edit id, version, timestamps, or errors by hand.
id: 2026-10-17-implement-sqlite-todo-journal
version: 1
status: in-progress
title: "Implement SQLite crash-recoverable todo journal"
priority: high
due: 2026-10-24
assignee: autodoc:claude
created: 2026-10-17T09:15:00+09:00
updated: 2026-10-17T10:02:11+09:00
status_changed: 2026-10-17T09:30:00+09:00
tags:
  - autodoc
  - storage
adr:
  - $KB_ROOT/adrs/1791532020-autodoc-owns-the-todo-system.md
review:
  - $KB_ROOT/reviews/lector/2026-10-11-autodoc-todo-ownership-adr1791532020-r7-review.md
blocked:
  - 2026-10-11-autodoc-module-convention-foundation
---

# Implement SQLite crash-recoverable todo journal

Ensure every store mutation records a `pending` row before touching `vfs/local`...
```

### Field Ownership Rules

AutoDoc partitions frontmatter fields into three strict categories:

1. **Daemon-Managed Fields (`managed: true`)**: `id`, `version`, `created`, `updated`, `status_changed`, `completed_at`, `archived_at`, and `errors`. Clients and humans must **never** edit these by hand. `todos.update` rejects patches that include them (`todos.invalid`, `-32073`).
2. **Verb-Managed Lifecycle Fields**:
   - `status` — Transitioned via `todos.status` (or implicitly from `open` to `in-progress` on `todos.assign`). Moving a task to `completed` stamps `completed_at`; moving it to `archived` stamps `archived_at` (preserving `completed_at` if it came from `completed`) and relocates the file into `archived/YYYY/MM/`.
   - `assignee` — Mutated via `todos.assign`. Per Decision D1 of ADR 1791532020, assigning an `open` task atomically transitions it to `in-progress` in the same write and records an `assign` journal event so notification consumers (such as `auto-core`) wake the target agent. Editing `assignee:` directly in YAML is treated as a metadata-only hand edit and never triggers spurious agent notifications.
3. **Hand-Editable Fields**: `title`, `description` (the Markdown body), `priority` (`critical`, `high`, `medium`, `low`), `due` (`YYYY-MM-DD`), `tags`, `adr`, `review`, and `blocked`. Users and agents may edit the Markdown body directly on disk or pass a partial map to `todos.update` with the task's current `rev` token.

---

## 3. Portable Symbolic Path Rewriting (`$KB_ROOT` and `$PROJECT_ROOT`)

A task's `adr`, `review`, and `blocked` fields link operational work to durable architectural artifacts. Because absolute paths break when a repository or Knowledge Base is synced across machines (for example, `/Users/alice/...` on macOS versus `/home/alice/...` on Linux), AutoDoc automatically rewrites absolute paths into portable symbolic variables on `todos.add`, `todos.update`, and `todos.refresh`.

When you pass an absolute path in `adr` or `review`, the symbolizer checks the owning workspace's variable table (`todos.vars` / `workspace.vars`, ADR 1792132105) and rewrites the path using the **longest matching prefix** among:

- `$KB_ROOT` — The Knowledge Base workspace root governing the store.
- `$PROJECT_ROOT` — The enrolled project root owning a project-scoped store (note: legacy `$WORKSPACE` is retired in ADR 1792132105 and remains unresolved unless explicitly defined).
- User-defined variables configured via `workspace.set_var` or `todos.set_var` (stored portably in `.autodoc-vars.toml` for workspace-scoped variables or locally for machine-scoped overrides).
- `$HOME` — Fallback home directory variable.

> [!IMPORTANT]
> **Durable documents belong under `$KB_ROOT`:** While AutoDoc can symbolize project files under `$PROJECT_ROOT`, ADRs and code reviews should always reside in the Knowledge Base (`$KB_ROOT/...`) so they resolve across every project worktree. Never write retired `$WORKSPACE/...` references. Furthermore, every entry in `blocked:` must be a resolvable task `id` (such as `2026-10-11-autodoc-module-convention-foundation`) or a `$KB_ROOT/...` document reference—never free-text prose. During `todos.refresh`, the validator checks every reference and populates `errors[]` with structured diagnostics (`not-found` or `unresolved-variable`) while preserving the original `detected` timestamp for unchanged errors.

---

## 4. The 3-Stage Crash-Recoverable SQLite Journal (`sqlitejournal`)

Because task transitions often span multiple filesystem operations—such as writing a destination file in `in-progress/` and unlinking the source file in `open/`—an unexpected power loss or `SIGKILL` mid-operation could otherwise leave duplicate task IDs, lost files, or unsent assignment notifications.

AutoDoc solves this in `core/todo/sqlitejournal` (SQLite migration `000017`), which declares six tables through the store's `store.Declare` seam: `todo_store`, `todo_root`, `todo_var`, `todo_change`, `todo_snapshot`, and `todo_consumer`. Every table keys on the store's random, immutable `todo_store.id` rather than its directory path, so moving a store via `todos.move_store` preserves its entire journal history, snapshots, and consumer cursors in a single transaction.

![Task Status Lifecycle and 3-Phase SQLite Crash-Recovery Journal](todo-lifecycle-and-journal.mmd)

### How Journaled Mutations and Recovery Work

Every write operation acquires the per-store mutex, verifies the caller's SHA-256 `rev` against a fresh read from `vfs/local`, and executes a strict 3-stage protocol:

1. **Stage 1 (`pending`)**: Before touching the filesystem, the daemon inserts a `todo_change` row with `state = 'pending'`, recording the monotonic `seq`, operation kind (`create`, `update`, `assign`, `move`, `delete`, `move_store`), `from_path`, `to_path`, `from_rev`, and deterministic target `to_rev`.
2. **Stage 2 (Filesystem Execution & `staged` Deletes)**:
   - For **creates and in-place updates**, the daemon writes a sibling temporary file via `os.Root`, `fsync`s the file and parent directory, and atomically renames it into place (`RENAME_NOREPLACE` for creates).
   - For **bucket moves** (`todos.status`, `todos.assign` on an open task, or 28-day auto-archive), it writes and `fsync`s the destination bucket file first, then removes the source file.
   - For **deletes** (`todos.remove`, `todos.remove_malformed`), it first renames the target file into `<store>/.trash/<seq>-<file>`, transitions the SQLite journal row to `state = 'staged'`, and then unlinks the trash file. The intermediate `staged` state distinguishes a daemon-initiated deletion from an external manual unlink.
3. **Stage 3 (`committed`)**: In a single SQLite writer transaction, the daemon marks the `todo_change` row `committed` and updates `todo_snapshot(store, path, id, rev, status, assignee, fields)` with per-field digests.

If the daemon crashes at any point, recovery runs automatically the next time the store is opened—before any new read or write is served—by comparing the on-disk file revisions against `from_rev` and `to_rev`:

- If a `pending` move already wrote `to_path` with `to_rev` and `from_path` still holds `from_rev`, recovery unlinks `from_path` and commits the journal row.
- If a `pending` delete already moved the file into `.trash/<seq>-<file>` with `from_rev`, or if the row reached `staged`, recovery removes the trash file and commits.
- If an external process modified the file while a row was `pending` (so neither `from_rev` nor `to_rev` matches), recovery never guesses: it deletes the uncommitted `pending` row, logs an ambiguity event in `sys.events`, and lets the snapshot diff record the on-disk state as a `hand = true` update.

![Workspace Task Velocity and Burndown Across Six Weekly Sprints](task-velocity-and-burndown.chart.json)

---

## 5. The Interactive Todos Panel (`SPC x`)

In the AutoDoc TUI and GUI, pressing `SPC x` (or selecting **Todos › Open todos…**) opens the native Todos Panel—a resizable, dockable drawer (`65%` window width by `80%` height by default) implemented as a modular TUI `Feature` (`tui/todos.go`).

### Navigating Stores, Buckets, and Variables

- **Store Selector Bar (`S`, `N`, `O`, `L`, `R`, `M`)**: Along the top of the panel, a dropdown (`S`) switches between the active workspace's store and any project stores enrolled via `todos.register`. Action buttons beside it create a **New** store (`N`), **Load** an existing project folder (`O`), edit the store's display **Label** (`L`, persisted in preferences by stable store ID so it survives directory moves), **Refresh** and reconcile the store (`R`), or **Move** the store to a new directory (`M`).
- **Six Bucket & Variable Tabs (`1`–`6`, `[` / `]`)**:
  - Tabs `1` through `4` display numbered tables for **Open**, **In progress**, **Deferred**, and **Completed** tasks.
  - Tab `5` (**Archived**) displays a two-level browser: the left list shows `YYYY/MM` archive partitions with task counts; pressing `Enter` on a month loads its tasks into the table, and `Backspace` returns focus to the month list.
  - Tab `6` (**Variables**) inspects the store's effective symbolic path variables (`$KB_ROOT`, `$PROJECT_ROOT`, `$HOME`, and custom variables), showing each variable's resolved path and scope (`e` sets a workspace-scoped variable in `.autodoc-vars.toml`; `m` sets a machine-local override).
- **Details Pane & Contextual Action Row (`i` / `o`)**: Below the table, a toggleable Markdown preview renders the selected task's metadata, any validation `errors[]`, and its description body. The bottom action row dynamically surfaces only valid operations (`Add`, `Status…`, `Assign`, `Remove`), hiding all write actions when viewing an unclaimed pre-cutover store. Pressing `?` opens a grouped keybinding overlay card.

---

## 6. Using `todos.*` RPC Verbs from CLI and Scripts

Every action in the Todos Panel maps 1-to-1 to a typed `todos.*` contract registered in `core/todo/module.go` (Protocol 20+). The first parameter of every store-scoped verb is `at`: either a workspace name string (e.g., `"kb"`) or a project root object (`{"root": "/abs/path/to/project"}`).

```sh
# 1. List active tasks in workspace "kb" filtered by tag and priority
autodoc --call todos.list '["kb", {"tag": "autodoc", "priority": "high"}]'

# 2. Fetch a single task to obtain its current SHA-256 rev token
autodoc --call todos.get '["kb", "2026-10-17-implement-sqlite-todo-journal"]'

# 3. Update hand-editable fields conditionally at the read rev
autodoc --call todos.update '[
  "kb",
  "2026-10-17-implement-sqlite-todo-journal",
  "a94f8c2e1b0d...",
  {"priority": "critical", "adr": ["/home/johno/.config/nvim/.auto-agents-config/kb/adrs/1791532020-autodoc-owns-the-todo-system.md"]}
]'

# 4. Transition status to completed (moves file to completed/ and stamps completed_at)
autodoc --call todos.status '["kb", "2026-10-17-implement-sqlite-todo-journal", "e31b7d4c9a12...", "completed"]'

# 5. Reconcile misplaced files, validate refs into errors[], and run 28-day auto-archive
autodoc --call todos.refresh '["kb"]'
```

### Pull-Based Consumer Cursors (`todos.changes` and `todos.ack`)

Because the AutoDoc daemon never pushes unsolicited messages over RPC sockets, external bridges (such as Neovim's `auto-core` assignment notifier) consume mutations using a durable pull cursor:

```sh
# Pull up to 50 committed changes after auto-core's last acknowledged sequence number
autodoc --call todos.changes '["kb", "auto-core", 50]'

# Acknowledge sequence 148 after delivering mailbox notifications
autodoc --call todos.ack '["kb", "auto-core", 148]'
```

Journal rows are retained for at least 30 days and are pruned only after every active consumer has acknowledged them. If a consumer stays silent for more than 90 days, it is automatically unregistered and receives a full `resync` snapshot the next time it calls `todos.changes`.

![Complete Reference of todos.* RPC Verbs and .todo-list/ Frontmatter Fields](todo-verbs-and-fields.csv)

---

## Summary

By consolidating task storage into `core/todo` and `core/todo/sqlitejournal`, AutoDoc gives human developers and AI agents a human-readable Markdown task store backed by database-grade concurrency, symbolic path portability, and crash recovery. In Chapter 11, we will see how the Todos Panel connects directly to AutoDoc's embedded terminal tabs to hand off tasks to live CLI coding agents with a single keystroke.

---

[← Previous: Chapter 09: Code Symbol Indexing](./09-code-symbol-indexing.md) | [Index](./README.md) | [Next: Chapter 11: Terminal Tabs & Agent Handoff →](./11-terminal-tabs-and-agent-handoff.md)

