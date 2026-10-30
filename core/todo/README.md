# todo

AutoDoc's todo system: task stores, their format, the rules, and every write ([the decision](../../docs/decisions/todos.md#the-daemon-owns-the-todo-system)).

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
A store is the daemon's from its first write: the write claims it, as `Claim` does, marker
included, and `Add` makes a missing store first ([the decision](../../docs/decisions/todos.md#a-store-is-claimed-by-its-first-write)). `Release` gives a store
back, and records it: writes are then refused (`ErrUnclaimed`), a write never claiming it again,
until `Claim`. A new project: `Register(root)`, then its first write (or `Scaffold(at)`). Until
then its store reads as an empty one (`List`, `Get`, `Changes`), and the reads write nothing; a
root no one registered answers `ErrNotAStore`.

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
| relative refs are the KB's (D2); absolute refs written as `$KB_ROOT`, a variable of the store's workspace or `$HOME`, longest prefix | `Add`, `Update`, `Refresh` |
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

## Variables

A ref's `$NAME/…` resolves with the variables of the workspace (the KB) the store belongs to (ADR
1792132105), the same from every route to the store, and the index resolves every other document of
the workspace with the same ones (`EffectiveVars`, `AllEffectiveVars`).

- **A store's workspace** is recorded when the store is: the deepest workspace containing it, kept
  when a workspace is later added inside it. A store outside every workspace has none until
  `SetKB` (`todos.set_kb`) links it; `SetKB` also moves a store. `Owner(path)` (`workspace.of`) is
  the one owner rule: a store's file, its store's workspace; any other file, the deepest workspace
  containing it.
- **A name's value**, first match winning: this machine's row for the workspace (absolute, never
  travels); the KB's `<root>/.autodoc-vars.toml` (paths relative to the KB root, carried by git; an
  absolute value there is refused and reported); the derived `$KB_ROOT` (the workspace root) and
  `$HOME`; a global variable; the environment. `$PROJECT_ROOT` is a user variable; `$WORKSPACE` is
  no longer derived (`todos.migrate_refs` moves its refs).
- **`SetVar`**: scope `kb` (`store` for an `At`) writes the KB's file, relative; scope `machine` a
  row, and `MachineOnly` declares the name in the file's `machine` list first, so other machines
  report it as a value to set, never a ref error.
- **The migration** (`MigrateVars` at start, and `fold` on a workspace's first read): each store's
  old `.autodoc-vars.toml` and per-store machine values move into the KB's file and this machine's
  rows, and the store's file is renamed `.autodoc-vars.toml.migrated` only after. Stores of one
  workspace that set a name differently are a conflict, reported (`todos.vars_conflict`) and left in
  place; the name is unresolved below this machine's row until a KB-file value or a machine-only
  declaration settles it.

## Cutover

`Claim(ctx, at, ClaimOpts{LegacyWorkspace: dir})` claims the store and then, once, rewrites the
refs auto-core wrote against its own `$WORKSPACE` (Neovim's project root, `dir`): each `adr` or
`review` ref `$WORKSPACE/<rest>` that the store's home (its parent) lacks and `dir` holds becomes that
path, symbolized again (`$HOME/…` for a root under home). A ref that resolves nowhere stays. Each
task is one journaled `refresh` change with the reason `cutover: $WORKSPACE was <dir>`. This pass
reads archives too, because a claim asks for it explicitly; a second one rewrites nothing.

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
