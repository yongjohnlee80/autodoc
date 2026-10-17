---
title: "Chapter 13: Concurrency Guarantees and Safe Multi-Agent Writes"
type: reference
status: active
created: 2026-10-17
tags: [autodoc, reference-book, concurrency, vfs, cas, follow, sqlite, tui-tickets]
abstract: "How AutoDoc prevents silent data loss, torn reads, missed filesystem events, database corruption, and stale UI updates when humans, Neovim, and multiple background AI coding agents read and write the same workspace concurrently."
---

# Chapter 13: Concurrency Guarantees and Safe Multi-Agent Writes

In a modern multi-agent engineering workflow, a single workspace directory is rarely touched by just one process. At any given second, a human architect may be editing an ADR in AutoDoc's GUI (`--gui`), a Neovim buffer may be updating task links via `autodoc.nvim`, and three autonomous coding agents (Claude Code, Gemini CLI, and Codex) may be concurrently patching documentation, transitioning `.todo-list/` tasks, and running `git checkout` or `git rebase` across worktrees.

Without strict concurrency engineering at every layer—from kernel file descriptors up to UI rendering callbacks—concurrent multi-agent access causes four classic failure modes:

1. **Silent Lost Updates (The ABA / Last-Writer-Wins Hazard)**: Agent A and Agent B read the same file; Agent A writes an update; Agent B writes its own update milliseconds later and silently overwrites Agent A's work.
2. **Torn Reads and Partial Writes**: An indexer or reader observes a half-written file on disk while another process is still flushing bytes.
3. **Startup Blind Spots**: A file changes while the indexer's initial directory walk is in progress, leaving the search index permanently stale until the next restart.
4. **Out-of-Order UI Responses**: A slow background RPC from an earlier workspace or buffer arrives after the user has switched views, painting stale data over the active screen.

AutoDoc eliminates all four failure modes through five defense-in-depth invariants spanning `golib/vfs`, `core/docs`, `core/todo`, `core/follow`, `core/store`, and `tui`.

---

## 1. Torn-Free Reads (`stat-read-stat`) and Atomic Conditional Writes (`golib/vfs`)

AutoDoc's core packages (`core/docs`, `core/todo`, `core/index`) never call Go's `os.*` functions directly (ADR 1791719881 §4.9). Instead, every workspace root and `.todo-list/` directory is opened outside `core/` as a confined `golib/vfs/local` filesystem backed by `os.Root`. Every path is resolved relative to the pinned root directory file descriptor, making it impossible for a malformed path, `..` segment, or symlink inside the workspace to escape the root boundary.

### The `steady` Stat-Read-Stat Loop

When `core/docs` reads a file (`Docs.Read` -> `Docs.steady` in `core/docs/docs.go`), it must return both the file's `Content` bytes and the exact `vfs.Version` token representing those bytes. If an external editor or agent modifies the file while `io.ReadAll` is mid-flight, pairing the pre-read `Version` with mutated bytes (or the post-read `Version` with stale bytes) would poison subsequent conditional writes.

`Docs.steady` prevents torn reads using a bounded **stat-read-stat** loop:

1. Call `d.fsys.Stat(ctx, path)` to capture `before` (`before.Version`, `before.Size`, and regular-file check) and reject files exceeding `MaxSize` (`4 MiB - 64 KiB`).
2. Read the file bytes via `io.LimitReader(r, MaxSize+1)` to detect whether the file grew past `MaxSize` after the first `Stat`.
3. Call `d.fsys.Stat(ctx, path)` again to capture `after`.
4. Accept the read **only if `after.Version == before.Version` and the read completed whole**. If the version moved in between, `steady` retries up to 5 times before returning `errs.ErrTimeout` (`"docs: <path> keeps changing while it is read"`).

### Optimistic Concurrency Control (CAS) and Atomic `.part` Renames

Every mutation through `doc.write`, `doc.write_raw`, `doc.patch`, `doc.remove`, `file.write`, and `todos.*` requires the caller to pass the version token (`version` or SHA-256 `rev`) it observed when reading the document.

![Compare-And-Swap (CAS) Concurrent Write Protocol Between Two Coding Agents](concurrent-write-cas-protocol.mmd)

Under the hood, `Docs.write` delegates to `vfs.ConditionalWriter` (`WriteFileIf`) when updating an existing file, or `vfs.ExclusiveCreator` (`CreateExclusive` with `want == ""`) when creating a new file:

- **Pre-Commit Version Verification**: `vfs/local` checks that the target file's current version matches `want` (and for `todos.*` mutations, `core/todo` holds the per-store mutex, re-reads the file bytes, and verifies the exact SHA-256 `rev` digest, catching even sub-millisecond hand edits that preserve file size and `mtime`).
- **Atomic Sibling Temp-File Rename**: Writes never truncate or overwrite the live file in place. Instead, `vfs/local` writes the new bytes to a sibling temporary file (`.part`), calls `fsync` on the temporary file descriptor so all data blocks are on stable storage, executes an atomic POSIX `renameat` (`RENAME_NOREPLACE` for exclusive creates or `RenameNoReplace`), and `fsync`s the parent directory.
- **Deterministic Conflict Refusal (`-32062` / `-32071`)**: If Agent A commits `sha256-2` while Agent B is still preparing a write against `sha256-1`, Agent B's `doc.write` or `doc.patch` is refused before rename with `*vfs.ConflictError` (wire code `-32062 rpc.conflict` for documents, or `-32071 todos.stale` for tasks). Agent B's contract (`Next`) instructs it to call `doc.read` (or `todos.get`), merge its changes onto `sha256-2`, and retry.
- **The Post-Commit Sentinel (`ErrCommitted`, `-32066`)**: What if the atomic `renameat` succeeds on disk, but a subsequent metadata step (such as the post-rename `Stat` that reads the new version) fails? Returning a generic error would cause the client to re-send its write with the old `want` version and hit a mysterious conflict. AutoDoc catches `vfs.ErrCommitted` in `committed()` (`core/docs/docs.go`) and returns `-32066 rpc.committed` with an empty version (`"the write landed, then a follow-up step failed: read the document and compare"`), telling the client that its bytes are already on disk.

---

## 2. The `watch-before-scan` Follower Invariant (`core/follow`)

Because coding agents and humans also edit files directly on disk using external tools (`vim`, `sed`, `git checkout`, `git pull`), AutoDoc continuously synchronizes each workspace's index with its filesystem root via `core/follow` (`Follower.Run` in `core/follow/follow.go`).

A naive file indexer walks the directory tree first and starts its `inotify` / `FSEvents` watcher second. That ordering contains a fatal race: if an agent modifies `adrs/0040.md` *after* the walker has already visited `adrs/` but *before* the watcher is registered, the modification is missed by both the scan and the watcher.

AutoDoc enforces the **`watch-before-scan` invariant** (`core/follow/follow.go:161-181`):

```go
// core/follow/follow.go — Follower.Run
events, mode, err := f.watch(ctx) // 1. Register recursive watcher FIRST
if err != nil {
    f.setFollowing(Degraded, err)
    f.reconcile(ctx, ".")         // Fallback: full reconcile + degraded retry
    f.degraded(ctx, backoff)
    ...
}
f.setFollowing(mode, nil)
f.reconcile(ctx, ".")             // 2. Walk and reconcile the tree SECOND
f.drain(ctx, events)              // 3. Drain buffered & live watch events THIRD
```

1. **Watch First (`f.watch`)**: The follower subscribes a recursive kernel watch (`w.Watch(ctx, ".", vfs.Recursive(), vfs.SkipDirs(f.opts.excluded))`)—or a `vfs.Poll` stream on platforms/filesystems without kernel events—*before* inspecting a single file. Excluded directories (`.git/**`, `**/node_modules/**`) are skipped at the watcher boundary so Git packfile churn never floods the event channel.
2. **Reconcile Second (`f.reconcile`)**: While incoming filesystem events buffer safely in the `events` channel, `reconcile` walks the workspace tree and compares each eligible file's on-disk `vfs.Version` against `store.Version(ctx, path)`, queueing only new, changed, or deleted paths via `queue.Touch(path)`.
3. **Drain Third (`f.drain`)**: Once the initial walk finishes, `drain` processes the buffered events. Because indexing is idempotent on `(path, vfs.Version)`, any event that fired during `reconcile` either confirms the version already indexed or triggers an immediate re-read if the file changed a second time.
4. **Resilient Subtree Backoff & Degraded Recovery**: If a subtree is temporarily unreadable during a scan, `Follower` preserves its previously indexed documents, records the path in `Status.Retrying`, and schedules an event-driven `time.Timer` (`minBackoff = 1s`, doubling up to `maxBackoff = 1m`) inside `drain` without blocking live watch events.

![Watch-Before-Scan Index Convergence Latency Under Burst File Writes](watcher-convergence-latency.chart.json)

---

## 3. Single-Writer SQLite Lease and Connection Serialization (`core/store`)

All workspaces, document chunks, link graphs, embedding vectors, preferences, and `sqlitejournal` todo tables reside in a single SQLite database (`$XDG_DATA_HOME/autodoc/autodoc.db`). To prevent database corruption or split-brain daemons when multiple CLI commands, Neovim instances, and GUI windows start simultaneously:

- **Kernel `flock` Lease with Device/Inode Verification**: When `autodoc --serve` (or an autostarting client) opens `core/store`, it acquires an exclusive `flock` on the store's lease file, verifies that the locked file descriptor's `(device, inode)` matches the path on disk (preventing unlink/recreate races), and writes a `lease-info` file recording the active socket endpoint and PID. If a second `autodoc --serve` is launched against the same store, it probes the recorded endpoint, sees the healthy daemon, reports its address, and exits `0`.
- **Single Writer Connection + Concurrent WAL Readers**: Inside `core/store/store.go`, AutoDoc opens SQLite in Write-Ahead Logging (WAL) mode with **exactly one writer connection** and a pool of concurrent reader connections. Every index commit, preference update, and `sqlitejournal` state transition is serialized through the single writer connection, eliminating `SQLITE_BUSY` deadlocks while allowing `search.query` and `graph.*` reads to execute in parallel without blocking writes.
- **RPC Session `sys.hello` Barrier (`rpc/hello.go`)**: On each Unix socket connection, `golib/rpc` dispatches requests concurrently on goroutines, but `rpc/hello.go` gates the connection's read loop behind the session's initial `sys.hello` handshake (up to 3 seconds). Clients can pipeline their first verb immediately after `sys.hello` without racing protocol version negotiation.

---

## 4. UI Async Ticket Invalidation (`TicketSession`, `TicketWorkspace`, `TicketFile`, `TicketLocal`)

Concurrency hazards do not stop at the daemon socket. In the AutoDoc TUI and GUI (`tui/`), **no daemon RPC ever runs on the UI render loop** (`tui/work.go`). Every feature dispatches background work via `Core.Go(fn, then)` and receives the result back on the single UI event loop via `h.p.Post`.

Suppose a user opens Workspace A, triggers a slow query or task load, and immediately switches to Workspace B (`Ctrl+W`) before Workspace A's RPC finishes. If Workspace A's callback blindly updates `App.*` state when it arrives 50ms later, the screen will display Workspace A's files inside Workspace B.

AutoDoc solves this uniformly across every UI feature using `Core.Ticket(seq, scope)` (`tui/feature.go:438-537`):

```go
// tui/feature.go — Host.ticket
gen, epoch, file, page := h.session.Gen(), h.epoch, h.file.gen, h.active
return Ticket{current: func() bool {
    if scope == TicketLocal {
        return seq == nil || *seq == n
    }
    if seq != nil && *seq != n || h.session.Gen() != gen {
        return false
    }
    switch scope {
    case TicketSession:
        return true
    case TicketWorkspace:
        return h.epoch == epoch
    }
    return h.epoch == epoch && h.active == page && h.file.gen == file
}}
```

When a feature initiates an async operation, it requests a `Ticket` bound to a per-operation sequence counter (`*seq++`) and one of four invalidation scopes:

1. **`TicketLocal`**: Invalidated only when a newer request increments `*seq` (used for purely local UI timers and terminal work independent of the daemon connection).
2. **`TicketSession`**: Invalidated if `*seq` advances **or** the daemon connection reconnects (`h.session.Gen() != gen`), used for global state such as `todos.stores` and `preference.list`.
3. **`TicketWorkspace`**: Invalidated on sequence advance, session reconnect, **or** a workspace switch (`h.epoch != epoch`), used by pickers (`agentAssignPicker`), workspace file listings, and `todos.assign` handoffs.
4. **`TicketFile`**: Invalidated on sequence advance, session reconnect, workspace switch, **or** when the active editor buffer or file generation changes (`h.active != page || h.file.gen != file`), used by live frontmatter validation, outline updates, and the relations drawer.

When the background goroutine posts its result back to the UI loop, the callback checks `if !tk.Current() { return }` as its very first line—silently discarding any superseded answer before a single QML property is touched.

![Summary Reference of Concurrency Guards Across All AutoDoc Layers](concurrency-guards-summary.csv)

---

## Summary

From `os.Root`-confined `stat-read-stat` loops and SHA-256 conditional renames in `golib/vfs`, to the 3-stage `sqlitejournal`, the `watch-before-scan` follower invariant, the single-writer SQLite lease, and scoped UI `Ticket` guards, AutoDoc is architected from the ground up for high-concurrency multi-agent collaboration. Humans and AI coding agents can read, search, edit, and hand off tasks across the same workspace at full speed—confident that every conflict is caught deterministically and no work is ever silently lost.
