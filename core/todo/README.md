# todo

AutoDoc's todo system: task stores, their format, the rules, and every write (ADR 1791532020).

```go
import "github.com/yongjohnlee80/autodoc/core/todo"

m := todo.New(roots, disk, journal, todo.WithClock(now), todo.WithHome(home), todo.WithEvents(record))
tasks, err := m.List(ctx, todo.At{Workspace: "kb"}, todo.ListOpts{})
```

## Ports

- **`Roots`**: the daemon's workspaces, name to root.
- **`Filesystems`**: opens a directory as a golib `vfs.FS` and gives its canonical path. The
  package never touches the disk itself; file content goes through `core/docs`.
- **`Journal`**: the change log, the snapshot, consumers' cursors, and the store and root
  records. `core/store` implements it on SQLite; `todotest.Journal` in memory.

## Where

Every verb names its store first with an `At`: a workspace name, or `{root = "/abs"}` for a
project that is not a workspace. A root resolves to its `SetDir` override, else
`<root>/.todo-list`. A store gets a stable id the first time a root resolves to it.

Writable stores are a workspace's or a `Register`ed root's `.todo-list`, and `SetDir` overrides.
Writes wait for `Claim` (the cutover) and stop again at `Release`. A new project:
`Register(root)`, `Claim(at)`, `Scaffold(at)`.

## Format

`formats/v<N>.yaml` is a `core/schema` file per format version; a task's `version:` picks it. v1
is auto-core's schema v1 without automation: its fields, enums, required and managed fields, and
`order`. The schema says which fields and in what order; the code says how (yaml.lua's quoting,
`|` blocks, lists, the managed header) and the rules a schema cannot say (timestamps per status,
the timestamp shape, a file-safe id).

- Decoding refuses anchors, aliases, tags, merge keys and duplicate keys; `null`, `~` and an
  empty value are absent. A file using removed automation is listed as malformed and never
  rewritten or deleted.
- Rendering is auto-core's `md.encode`, byte for byte. Reading never rewrites a file.

## Rules

| Rule | Where |
|---|---|
| any status to any status; the same is a no-op | `Status` |
| `completed` stamps `completed_at`; `archived` stamps `archived_at`, keeping `completed_at` only from `completed`; others clear both | `Status`, refresh |
| completed for more than 28 days (calendar days) becomes archived, into `archived/YYYY/MM` of `archived_at` | `Refresh` |
| assigning an open task moves it to in-progress (D1); creating one with an assignee is an assignment too, logged as an `assign` | `Assign`, `Add` |
| `errors[]` for refs that do not resolve or exist and unknown `blocked` ids, `detected` kept | `Refresh` |
| relative refs are the KB's (D2); absolute refs written as `$KB_ROOT`, `$WORKSPACE`, a user var or `$HOME`, longest prefix | `Add`, `Update`, `Refresh` |
| ids `<date>-<slug>`; a caller's id is file-safe | `Add` |
| managed fields, status and assignee refused by `Update` | `Update` |

`Refresh` reads the active buckets only: archives are never rewritten by it, so an archived
task's `errors[]` reflect the time it was archived.

The daemon also refreshes each claimed store once a day by itself (`RefreshDaily`, run for as long
as it serves): at its start for every store whose last daily run is a day old or that never ran,
then a day after each run. The run is kept in the store's record (`RefreshedAt`), so a restart
neither repeats nor skips a day. A run that changed the store logs `todos.changed`, as the verb
does; one that failed is logged and recorded in `sys.events` as `todos.refresh_failed`, and is
tried again a day later.

## Cutover

`Claim(ctx, at, ClaimOpts{LegacyWorkspace: dir})` claims the store and then, once, rewrites the
refs auto-core wrote against its own `$WORKSPACE` (Neovim's project root, `dir`): each `adr` or
`review` ref `$WORKSPACE/<rest>` that the store's `$WORKSPACE` lacks and `dir` holds becomes that
path, symbolized again (`$HOME/…` for a root under home). A ref that resolves nowhere stays. Each
task is one journaled `refresh` change with the reason `cutover: $WORKSPACE was <dir>`. This pass
reads archives too, because a claim asks for it explicitly. It runs at the claim that claims the
store only: a claim of a store already claimed rewrites nothing and reads no archive.

## Change log

Every file change is journaled (`Begin` pending, file steps, `Commit` with the snapshot). After a
crash, recovery settles each pending row by what is on disk: a create, an in-place rewrite, a
move (write the new file, remove the old), a delete (into `.trash/<seq>-<file>`, `Stage`, remove)
or a store move. A file changed outside the daemon while its row was pending is ambiguous: the
row goes and the event says so.

Edits made by hand are found by diffing the files with the snapshot and logged as `update`
(fields named), never `assign`. Consumers pull committed changes with `Changes` and `Ack` them;
a consumer silent 90 days is unregistered and gets a resync when it returns. A resync is
delivered at least once, as a page is: the consumer is answered it again on every ask until it
acknowledges the resync's cursor, which registers it again there; what follows that cursor is
kept for it meanwhile. Changes older than 30 days that every consumer acknowledged are pruned.

## Tests

`todotest` has an in-memory Journal, a memfs-backed disk with fault injection and read counting,
and a workspace table. `TestParityWithAStoreCopy` and `TestReadingACopyNeverWrites` run against
a copy of a real store when `AUTODOC_TODO_PARITY_DIR` names it (`AUTODOC_TODO_PARITY_LUA_DIR`:
auto-core's own renderings to compare with; `AUTODOC_TODO_PARITY_REFRESH=1`: refresh it twice).
