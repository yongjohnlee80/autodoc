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

AutoDoc eliminates this drift by making its daemon (`core/todo`) the single authoritative owner of every `.todo-list/` store (ADR 1791532020). Whether a task is created from the interactive Todos Panel (`SPC x`), updated via the CLI (`autodoc --call todos.update`), scripted from Lua (`autodoc.todos.*`), or sent by another client over the daemon's socket, every operation flows through one pure-Go engine over `golib/vfs` and one crash-recoverable SQLite change log (`core/todo/sqlitejournal`).

---

## 1. On-Disk `.todo-list/` Layout and Bucket Hierarchy

A todo store is a plain directory of Markdown files with YAML frontmatter, located by default at `<workspace-root>/.todo-list/` (or at a custom directory registered via `todos.register` and `todos.set_dir`). Because tasks are ordinary `.md` files on disk, they can be versioned in Git, inspected with standard tools, and indexed by AutoDoc's search and link graph alongside the rest of the workspace.

Inside `.todo-list/`, a task's physical directory placement corresponds directly to its lifecycle `status`:

- `open/` — Newly created or queued tasks awaiting active work (`status: open`).
- `in-progress/` — Tasks actively being implemented by a human or assigned agent (`status: in-progress`).
- `deferred/` — Tasks intentionally paused or postponed (`status: deferred`).
- `completed/` — Finished tasks retained in the active working set for up to 28 days (`status: completed`).
- `archived/YYYY/MM/` — Completed tasks older than 28 days (or explicitly archived), partitioned by year and month of completion/archival so active listing remains instantaneous even as historical archives grow to thousands of files.
- `.autodoc-owner` — The durable ownership marker the daemon writes when it claims the store: at the store's first write when it lies at a registered root (an AutoDoc workspace, or a root `todos.register` adds), or by `todos.claim`.
  - A store `todos.release` gave back stays read-only: a write no longer claims it, and `todos.claim` takes it back. **If you released a store with `todos.release` before v0.3.0, release it again after upgrading:** a release made before then left no record, so the store's first write would claim it.
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
   - `assignee` — Mutated via `todos.assign`. Per Decision D1 of ADR 1791532020, assigning an `open` task atomically transitions it to `in-progress` in the same write and records an `assign` journal event so a notification consumer (an assignment notifier reading `todos.changes`) can wake the target agent. Editing `assignee:` directly in YAML is treated as a metadata-only hand edit and never triggers spurious agent notifications.
3. **Hand-Editable Fields**: `title`, `description` (the Markdown body), `priority` (`low`, `medium`, `high`; `normal` is the legacy spelling of `medium`: a task that has it is valid and reads as `medium`, and its next write writes `medium`), `due` (`YYYY-MM-DD`), `tags`, `adr`, `review`, and `blocked`. Users and agents may edit the Markdown body directly on disk or pass a partial map to `todos.update` with the task's current `rev` token.

A task's frontmatter is the todo format's, not a KB document type's: the editor and `index.documents {diagnosed: true}` check a task against the format, never against the workspace's schema (see the frontmatter-schemas reference).

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

In the AutoDoc TUI and GUI, pressing `SPC x` (or selecting **Todos › Open todos…**) opens the native Todos Panel, implemented as a modular TUI `Feature` (`tui/todos.go`). **The todos** (`SPC x`): one todo store at a time, its tasks a tab per status, as autofinder's
todos panel shows them, in a large panel at the left (about 65% of the window wide and 80% tall
until you size it; the size and the side you leave it at are kept).

### Navigating Stores, Buckets, and Variables

- **Store Selector Bar (`S`, `N`, `O`, `L`, `R`, `M`)**: Along the top, a dropdown
  chooses the store shown among the ones AutoDoc knows (the workspace's first), by its label or its
  name; under it, buttons make a new store (New), load one from a folder (Load), rename the store
  shown (Edit: its label, AutoDoc's own) and refresh it (Refresh), framed. A label is persisted in preferences by stable store ID, so it survives directory moves. `M` moves the store to a new directory.
- **Bucket & Variable Tabs (`1`–`6`, `[` / `]`)**: then the tabs, Open, In
  progress, Deferred, Completed and Archived, each a numbered table of its tasks; Archived lists
  its months, a month's tasks beside them.
  - Tab `5` (**Archived**) lists each `YYYY/MM` archive partition with its task count.
  - Tab `6` (**Variables**) inspects the store's effective symbolic path variables (`$KB_ROOT`, `$PROJECT_ROOT`, `$HOME`, and custom variables), showing each variable's resolved path and scope (`e` sets a workspace-scoped variable in `.autodoc-vars.toml`; `m` sets a machine-local override).
- **Details Pane & Contextual Action Row (`i` / `o`)**: Under the tabs the task under the cursor's details,
  then one row of the actions that apply: Status…, Assign and Remove on a task, and Add. The details pane renders the selected task's metadata, any validation `errors[]`, and its description body. A store
  is written from its first write, with no step first; only one released with `todos.release`
  shows none, only "read-only: this store was released (todos.release)".
  `?` opens the card of the panel's keys, by purpose; `?` again or `Esc` closes it.

| Key | In the todos panel |
| --- | --- |
| `Enter` | open the task's file; on Archived's months, open a month |
| `Backspace` | back to Archived's months |
| `1`–`6`, `[` `]` | a tab (`6` is Variables); the tab before, after |
| `i`, `o` | the details, shown or hidden |
| `a` | add a task |
| `d` | remove the task |
| `s` | Status…: a picker of every status (open, in-progress, deferred, completed, archived), the task's own marked; choosing one moves the task there, its tab showing with the cursor on it |
| `A` | assign the task to an agent |
| `S` | the store dropdown (`Enter` opens it) |
| `N`, `O` | a new store; load a store from a folder |
| `L`, `R`, `M` | rename (label), refresh or move the store |
| `?` | the card of these keys |

### Assigning a Task to an Agent (`A`)

**`A` assigns the task under the cursor to one of AutoDoc's own agents**:
pick the agent, then type an optional instruction (`Esc` cancels; empty is "Task assigned to
you."). The task's assignee becomes `autodoc:<codename>`, and an open task moves to in progress.
The agent is then handed the task in its terminal: its title, its file and the instruction. If
the agent isn't running, you're asked first, and it's handed over once it starts. If you decline,
or the agent can't take it, the task stays assigned and a notice says so; `A` again delivers it.
AutoDoc assigns only to its own agents. [Chapter 11 §3](./11-terminal-tabs-and-agent-handoff.md#3-the-one-key-agent-task-handoff-workflow-spc-x---a) walks through the handoff step by step.

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
  {"priority": "high", "adr": ["/home/me/kb/adrs/1791532020-autodoc-owns-the-todo-system.md"]}
]'

# 4. Transition status to completed (moves file to completed/ and stamps completed_at)
autodoc --call todos.status '["kb", "2026-10-17-implement-sqlite-todo-journal", "e31b7d4c9a12...", "completed"]'

# 5. Reconcile misplaced files, validate refs into errors[], and run 28-day auto-archive
autodoc --call todos.refresh '["kb"]'
```

### Pull-Based Consumer Cursors (`todos.changes` and `todos.ack`)

Because the AutoDoc daemon never pushes unsolicited messages over RPC sockets, an external consumer (an editor bridge, or an assignment notifier) reads mutations through a durable pull cursor, under a consumer name of its own:

```sh
# Pull up to 50 committed changes after the consumer's last acknowledged sequence number
autodoc --call todos.changes '["kb", "notifier", 50]'

# Acknowledge sequence 148 once its notifications are delivered
autodoc --call todos.ack '["kb", "notifier", 148]'
```

Journal rows are retained for at least 30 days and are pruned only after every active consumer has acknowledged them. If a consumer stays silent for more than 90 days, it is automatically unregistered and receives a full `resync` snapshot the next time it calls `todos.changes`.

![Complete Reference of todos.* RPC Verbs and .todo-list/ Frontmatter Fields](todo-verbs-and-fields.csv)

---

## Summary

By consolidating task storage into `core/todo` and `core/todo/sqlitejournal`, AutoDoc gives human developers and AI agents a human-readable Markdown task store backed by database-grade concurrency, symbolic path portability, and crash recovery. In Chapter 11, we will see how the Todos Panel connects directly to AutoDoc's embedded terminal tabs to hand off tasks to live CLI coding agents with a single keystroke.

---

[← Previous: Chapter 09: Code Symbol Indexing](./09-code-symbol-indexing.md) | [Index](./README.md) | [Next: Chapter 11: Terminal Tabs & Agent Handoff →](./11-terminal-tabs-and-agent-handoff.md)

