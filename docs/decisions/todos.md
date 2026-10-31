# Todo decisions

AutoDoc's daemon owns the `.todo-list` task stores: their format, their rules and every write. This
page records the decisions behind that system: how a store is owned and claimed, how changes are
logged and delivered, how tasks are indexed, how a task's `$NAME/…` refs resolve, and how a task is
handed to an agent. [Chapter 10 of the reference](../reference/10-todo-task-engine.md) describes the
system as a user sees it.

## The daemon owns the todo system

One Go package, `core/todo`, holds the task format and the rules, and the daemon is the only writer.
Every surface is a client of it: the Todos view, `autodoc --call todos.*`, Lua scripts
(`autodoc.todos.*`) and editor plugins. Before this, several tools each carried their own copy of
the rules, and the copies disagreed.

- Every verb names its store first with an `At`: a workspace name, or `{root}` for a project that
  is not a workspace. The store is the root's `todos.set_dir` override, else `<root>/.todo-list`.
- Writes are confined to the writable stores (a workspace's or a registered root's store, and
  overrides), go through `golib/vfs` as atomic renames, and are serialized by one mutex per store.
- Every task carries a `rev`, the hash of its file's bytes. A mutation names the `rev` it saw, and
  a mismatch is refused as `todos.stale` instead of overwriting a newer file or a hand edit.

In the code: `core/todo/doc.go`, `core/todo/manager.go`, `core/todo/module.go`, `lua/autodoc/todos.lua`

## A versioned format without automation

The task format is a schema file per version, `core/todo/formats/v<N>.yaml`, validated by the same
`core/schema` validator as a workspace's frontmatter. A task's `version:` picks its format. What a
schema cannot say (timestamps per status, YAML refusals, quoting and layout) stays in Go.

Task automation (the `automated` status and its fields) is removed. A file that still uses it is
listed as malformed, with that reason, and is never rewritten or deleted. Reading never writes: a
valid file keeps its bytes until a mutation or a refresh changes that task.

In the code: `core/todo/format.go`, `core/todo/formats.go`, `core/todo/formats/v1.yaml`

## Priority: low, medium, high

A task's priority is `low`, `medium` or `high`. `normal` is the legacy spelling of `medium`: the v1
schema declares it as an alias, so a file that has it is valid and reads as `medium`, and the next
write of that task writes `medium`. A refresh never rewrites a file for the spelling alone.

In the code: `core/todo/formats/v1.yaml`, `core/schema`

## Task rules and the daily refresh

The rules are decided once, in `core/todo`:

- any status may move to any status, and the same status is a no-op;
- `completed` stamps `completed_at`, `archived` stamps `archived_at`, and the others clear both;
- assigning an `open` task moves it to `in-progress` in the same write, from every surface;
  creating a task with an assignee counts as an assignment;
- a task completed for more than 28 days is archived into `archived/YYYY/MM`;
- relative refs resolve against the KB root only, and absolute refs are written back as variables,
  longest prefix first;
- `errors[]` records refs that do not resolve and unknown `blocked` ids.

Archiving and `errors[]` run in `todos.refresh`. The daemon also refreshes each claimed store once
a day by itself, for as long as it serves. The last run is kept in the store's record, so a restart
neither repeats nor skips a day.

In the code: `core/todo/rules.go`, `core/todo/ops.go`, `core/todo/daily.go`, `app/serve.go`

## A store is claimed by its first write

A store becomes the daemon's at its first write, when it lies at a registered root (an AutoDoc
workspace, or a root added with `todos.register`). The write records the claim and writes the
`.autodoc-owner` marker, as `todos.claim` does. `todos.add` on a registered root with no store yet
creates the store first, so a new workspace's first task needs no other step.

- A read of a registered root's store that does not exist yet answers as an empty store, and writes
  nothing: no scaffold, no claim.
- A store given back with `todos.release` is recorded as released. Writes to it are refused as
  `todos.unclaimed`, and only an explicit `todos.claim` takes it back; a write never does.
- A root no one registered is not writable.

AutoDoc is a standalone product: a user with no other tools creates a workspace and adds, updates
and completes tasks with no claim or setup step.

In the code: `core/todo/manager.go`, `core/todo/ops.go`, `core/todo/journal.go`, `rpc/server.go`

## A pull-based change log

The daemon never pushes, so todo changes are a durable log that clients pull and acknowledge. Every
file change is journaled: a pending row, then the file steps, then the commit with the task's
snapshot, so a row exists if and only if its file change happened. After a crash, recovery settles
each pending row by what is on disk, and a file that changed outside the daemon meanwhile is
recorded as ambiguous, never claimed as the daemon's own work.

- Hand edits are found by comparing files with their snapshots, and are logged as `update` changes,
  never as `assign`. A direct edit of `assignee:` is metadata only and notifies no one.
- A consumer keeps its cursor in the daemon: `todos.changes` returns committed changes after it,
  and `todos.ack` advances it once the consumer has durably handled them.
- A consumer silent for 90 days is unregistered and gets a resync when it returns. A resync is
  delivered at least once: it is answered again until the consumer acknowledges it.
- Rows are pruned only after 30 days, and only once every consumer has acknowledged them.

In the code: `core/todo/journaled.go`, `core/todo/ops.go`, `core/todo/sqlitejournal/journal.go`

## An assignment carries its assigner's scope

Every editor sharing a store reads the same change log, so a bare assignee such as an agent name
could be delivered by every editor that has an agent of that name. The assigner names its scope
instead, an opaque string such as its project, and AutoDoc carries it without reading it.

- `todos.assign` and `todos.add` take an optional `scope` of at most 256 bytes. It is kept on the
  assign change and on the task's snapshot row, and returned by `todos.changes` and in a resync.
- A scope belongs only to the assignment that set the assignee: any other change to the assignee,
  a hand edit included, clears it.
- A consumer that delivers only its own scope's assignments says so with `scoped` on
  `todos.changes`. One that does not is given each scoped assignment without its assignee, so it
  cannot deliver it everywhere. An assignment with no scope keeps its assignee.

In the code: `core/todo/journal.go`, `core/todo/types.go`, `core/todo/ops.go`, `rpc/server.go`

## AutoDoc's own todos view

The TUI and GUI show the todo stores in a native view, reached with `SPC x` or the Todos menu. It
shows one store at a time, its tasks a tab per bucket (Open, In progress, Deferred, Completed,
Archived) and a Variables tab. Active buckets list from the read cache; the Archived tab lists its
months with their counts and decodes a month only when it is opened, since archives grow to
thousands of files.

In the code: `tui/todos.go`, `tui/todos_status.go`, `tui/todos_vars.go`

## AutoDoc assigns only to its own agents

AutoDoc handles tasks; agents stay with the tool that runs them. The `assignee` field is free-form in
the daemon, and a client checks a name against its own agents. AutoDoc's Assign (`A` in the todos
view) targets AutoDoc's own agents and writes the assignee as `autodoc:<codename>`. An assignee
another tool wrote keeps that tool's form, and AutoDoc never delivers to it.

In the code: `tui/todos_assign.go`, `tui/agentinstances.go`

## Text reaches an agent only as a bracketed paste

A task handed to an agent goes as one bracketed paste, or not at all. To a program that has not
enabled bracketed paste, a paste arrives as keystrokes: its newlines are Enters, and its letters can
answer a select prompt one key at a time. Such a program gets nothing, and the user is told why.

- An agent the delivery has just started is not ready for bracketed pastes yet, so the paste is
  tried again until the program enables them, for at most 10 seconds.
- The same rule covers every text AutoDoc sends a terminal the user did not type, including Lua's
  `autodoc.agents.send`: its refusal is the script's error.

In the code: `tui/agentinstances.go`, `tui/terminalpanel.go`, `tui/luahost_test.go`

## A message to an agent is sent, never left for Enter

Anything the user sends an agent (a todo's assignment, a candidate's audit, a text through Lua's
`autodoc.agents.send` or a command built on it) goes through one agent messaging, and arrives sent:
the user never has to press Enter for it. The messaging has two implementations, tried in order:

- **The doorbell.** When the agent's CLI has a wake channel (Claude's socket, agy's bridge,
  OpenCode's plugin, Codex's app-server), the message is crew mail from `user`
  ([the user's own sends](crew.md#the-users-own-sends)), and the daemon rings the agent's CLI. A
  doorbell that doesn't fire leaves nothing sent and answers an error.
- **The base: typed and sent.** Otherwise, or when the doorbell didn't fire, the message is typed
  as one bracketed paste ([above](#text-reaches-an-agent-only-as-a-bracketed-paste)) and Enter
  follows it, after a short gap so the program reads them apart. A notice says the doorbell didn't
  ring, and why.

An Enter can't tell the agent's input from a permission or select prompt the agent is showing, so
the base never presses it into an agent the crew says waits on a prompt: the message stays typed, and
a notice asks the user to answer the prompt, then press Enter there. An agent with no crew session
reports no prompt.

A successful hand-over means the message reached the agent, not that it started on it.

In the code: `tui/agentmessaging.go`, `tui/agentinstances.go`, `tui/todos_assign.go`, `tui/candidate.go`

## Graph-only indexing

A path can be indexed graph-only: its document row, frontmatter and diagnostics, links, relations
and backlinks are kept, and it has no chunks. So it is never a lexical or semantic search hit and
costs no embeddings, while the Todos view and the graph still show its relations. A document's mode
is part of what the indexer compares, so a mode change re-indexes the file though its bytes did not
change, and a document leaving search loses its hits at once.

In the code: `core/index/mode.go`, `core/index/indexer.go`, `internal/daemon/configure.go`

## Which paths are graph-only

A path's mode is decided by the first rule it matches:

1. `exclude`: not indexed at all;
2. a `graph_only` pattern the user added: graph-only;
3. a path in a todo store: graph-only when the workspace's `todo_search` is off, else full;
4. `include`: full.

A todo store is known from the daemon's own store registry, not from a pattern, so the rule follows
`todos.set_dir` overrides wherever the store lives. The `graph_only` list starts with
`**/.todo-list/**`, to catch a store no route has resolved yet; that entry belongs to the todo rule,
so `todo_search` overrides it but never a pattern the user wrote. When the stores change (a store
registered, moved or removed) or `todo_search` is toggled, the affected paths are re-indexed with no
task file changing. The index reads the registry; it never calls into the todo module.

In the code: `internal/daemon/modes.go`, `internal/daemon/workspaces.go`, `core/workspace/spec.go`, `core/store/workspaces.go`

## Tasks stay in search by default

`todo_search` is on unless a workspace turns it off, so a workspace's tasks are in search by
default. Turning it off makes them graph-only: in the graph, out of search. The store keeps the
opt-out, so the schema's default is the product's default.

In the code: `core/store/workspaces.go`, `core/store/configure.go`, `internal/daemon/modes.go`

## Excluded todo stores are reported

An `exclude` pattern still wins over the todo rule, but it no longer fails silently. A workspace
whose excludes keep a todo store out of the index gets an event and a warning at each start, naming
the workspace and the patterns, and its settings offer to index the tasks graph-only instead. When a
file opened by absolute path lies under a workspace's root but is excluded there, the editor names
that workspace and the pattern, and offers to open the workspace's settings.

In the code: `internal/daemon/modes.go`, `rpc/workspace.go`, `tui/workspace.go`, `tui/outside.go`

## Task relations and external links

- `review` is a relation field, so a task's reviews appear in the Todos view and the graph.
- `blocked` is a relation whose key names the store: each task is indexed with the name
  `#todo:<store id>/<task id>`, and a `blocked` id is looked up in its own store, so two stores'
  identical ids never meet. It re-resolves as tasks are added, moved, archived or removed.
- A `$NAME/…` ref keeps its variable, and is expanded to an absolute path. Inside the workspace it
  links to that document; outside it, it is an external link with an absolute target and no
  document. A session below protocol 23 does not see these relations.

In the code: `core/index/mode.go`, `core/index/writer.go`, `core/index/graph.go`, `rpc/graph.go`, `tui/relations.go`

## Tasks are checked by the todo format

A task's frontmatter is the todo format's, not a KB document type's. A file in a store the registry
knows is never checked against the workspace's KB schema. Its diagnostics are the format's own: its
frontmatter validated by its format version's schema, and each entry of its `errors[]` as one
diagnostic. The index and `doc.validate` take the check from the path's mode, so the editor and
`index.documents {diagnosed: true}` agree. `core/index` does not depend on `core/todo`: the app hands
the registry `core/todo.CheckFile`, and the daemon passes it to the index with each store.

In the code: `core/todo/check.go`, `core/index/frontmatter.go`, `internal/daemon/modes.go`, `rpc/doc.go`

## Variables belong to the workspace

A workspace (a KB) owns its variables: a table of name to absolute path. A todo store belongs to
exactly one workspace and reads that workspace's variables, and so does every other document of the
workspace. This keeps one copy of the variables per KB, and lets a document outside any store
resolve `$NAME/…` refs too. `workspace.vars` and `todos.vars` answer the same table, each value with
its source.

In the code: `core/todo/storevars.go`, `core/todo/module.go`, `lua/autodoc/contracts.lua`

## One answer from every route to a store

A store can be reached by several routes: a workspace name, a folder root, an override. Its
variables are computed from the store, never from the route, so every route, and every client,
resolves and writes the same refs. Symbolization, validation and `errors[]` all use that one scope.
`todos.open` still accepts a `kb_root` from older clients and ignores it; a value an older client
recorded is carried over once and then cleared.

In the code: `core/todo/manager.go`, `core/todo/journal.go`, `core/todo/vars.go`

## Where a variable's value comes from

One precedence, first match winning, serves `workspace.vars`, `todos.vars`, indexing and symbolized
writes:

1. this machine's value, a row in the daemon's store: an absolute path that never travels;
2. the KB's file, `<KB root>/.autodoc-vars.toml`, carried by git: relative paths only, resolved
   against the KB root on each machine; an absolute value there is refused and reported;
3. the derived built-ins: `$KB_ROOT`, the workspace root, and `$HOME`, the daemon user's home;
4. a global user variable;
5. the environment.

The file's top-level `machine` list declares names whose value is set per machine: such a name with
no value here is reported to the user, never written into a task's `errors[]`. `$PROJECT_ROOT` is an
ordinary user variable, since the project a KB serves differs per KB and per machine.

In the code: `core/todo/storevars.go`, `app/serve.go`, `core/todo/journal.go`

## Which workspace a store belongs to

A store belongs to the deepest workspace whose root contains it. That is recorded once per store,
so a workspace added later inside it does not move it. A store outside every workspace has no
`$KB_ROOT` until `todos.set_kb` links it, and `todos.set_kb` also moves a store to another workspace.
Adding or removing a workspace re-reads the store registry, and the affected documents re-resolve.

In the code: `core/todo/storevars.go`, `core/todo/manager.go`, `core/todo/types.go`, `internal/daemon/workspaces.go`

## Resolving refs in every document

Every document's `$NAME/target` link or relation resolves with its own workspace's variables, not
the active workspace's, so a file opened beside another workspace keeps its links and graph. Inside
the workspace the target is a workspace document; outside, an external link; an unknown or unset
name stays unresolved and is never read from the workspace root.

- `workspace.of(path)` is the one owner rule: a path in a todo store belongs to its store's
  workspace; any other path to the deepest workspace containing it; otherwise to none, which
  resolves only through `$HOME`, the global variables and the environment.
- A workspace's variables are part of its documents' indexer version, so a variable change
  re-indexes exactly the documents whose refs use it, with no file edited.

In the code: `core/index/links.go`, `core/index/mode.go`, `internal/daemon/modes.go`, `core/todo/storevars.go`

## Retiring $WORKSPACE

`$WORKSPACE` meant two places: the Neovim plugins used it for the project root, and the daemon for a
store's parent folder. It is retired. It is no longer derived, and is an ordinary name, unresolved
unless someone sets it. The project root is `$PROJECT_ROOT`.

Old `$WORKSPACE/…` refs migrate through `todos.migrate_refs`: a plan lists every such ref and where
its target exists; a person reviews it; apply rewrites exactly the reviewed refs to
`$PROJECT_ROOT/…`, each rewrite journaled. A ref found in both places, or in neither, is never
proposed. Nothing rewrites these refs by itself, because which files exist today cannot tell what a
ref meant.

In the code: `core/todo/vars.go`, `core/todo/migraterefs.go`, `core/todo/cutover.go`, `core/todo/manager.go`

## Migrating store variable files

Variables once lived in a file beside each store and in per-store machine values. The daemon
migrates them once per workspace, as it starts serving and on a workspace's first read:

- a store file's relative values are rebased onto the KB root and written to the KB's file;
- absolute values become this machine's rows, and their names join the KB file's `machine` list;
- the store's file is renamed `.autodoc-vars.toml.migrated` only once every value has a
  destination; a failed write leaves it in place for the next start.

Two stores of one workspace that set a name differently are a conflict: reported, never picked, and
the name answers only to this machine's row meanwhile. A KB-file value, or declaring the name
machine-only, settles it; a machine row alone does not.

In the code: `core/todo/storevars.go`, `core/todo/daily.go`, `core/todo/types.go`

## A CLI call says who runs it

`autodoc --call` sends its pid, its working directory and, when it has one, its agent's mailbox id in
its hello. They go into the session's client token, so a todo event names who caused it.

In the code: `app/call.go`, `rpc/events.go`, `rpc/who_test.go`
