-- 000017 — the todo system's journal (ADR 1791532020 §2.5): core/todo's Journal port, kept by
-- core/store/todojournal.go.
--
-- A todo store (a .todo-list directory) is keyed by a stable id, random, given the first time a
-- root resolves to it, never by its path: moving a store rewrites todo_store.path and the roots'
-- overrides, and its claim, change log, snapshot and consumers' cursors stay where they are. No
-- table references todo_store: a row may outlive its store's record, as the port allows.
--
-- Every time is Unix nanoseconds, NULL for none (Go's zero time). A list or a map is JSON text,
-- NULL for none, as workspace.text_extensions is (000008).

-- A store: its path now, and whether AutoDoc has claimed it (the cutover's claim, §3).
CREATE TABLE IF NOT EXISTS todo_store (
  id         TEXT PRIMARY KEY,
  path       TEXT NOT NULL,
  claimed    INTEGER NOT NULL DEFAULT 0,
  claimed_at INTEGER,
  last_use   INTEGER
) WITHOUT ROWID;

-- A project root the daemon knows (§2.4): override is the store directory todos.set_dir chose,
-- '' for <root>/.todo-list; registered is when todos.register enrolled it; kb_root is $KB_ROOT for
-- a root that is not a workspace.
CREATE TABLE IF NOT EXISTS todo_root (
  root          TEXT PRIMARY KEY,
  override      TEXT NOT NULL DEFAULT '',
  registered    INTEGER,
  kb_root       TEXT NOT NULL DEFAULT '',
  last_resolved INTEGER
) WITHOUT ROWID;

-- A user's variable: a ref's $NAME, set by core/todo's SetVar.
CREATE TABLE IF NOT EXISTS todo_var (
  name  TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;

-- A store's change log. seq rises across every store and never repeats (AUTOINCREMENT), so a
-- consumer's cursor stays meaningful after rows are dropped or pruned. A row is pending from the
-- mutation's first file step until its commit; a delete is staged while its file is in the
-- store's trash. from_value and to_value are a status's or an assignee's old and new value, or a
-- store move's paths; fields is the JSON array of the fields an update changed.
CREATE TABLE IF NOT EXISTS todo_change (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  store      TEXT NOT NULL,
  id         TEXT NOT NULL DEFAULT '',
  kind       TEXT NOT NULL,
  op         TEXT NOT NULL,
  from_value TEXT NOT NULL DEFAULT '',
  to_value   TEXT NOT NULL DEFAULT '',
  fields     TEXT,
  reason     TEXT NOT NULL DEFAULT '',
  hand       INTEGER NOT NULL DEFAULT 0,
  from_path  TEXT NOT NULL DEFAULT '',
  to_path    TEXT NOT NULL DEFAULT '',
  from_rev   TEXT NOT NULL DEFAULT '',
  to_rev     TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL CHECK (state IN ('pending', 'staged', 'committed')),
  at         INTEGER
);
-- A store's pending rows, its head, a page after a cursor, and the rows a prune takes.
CREATE INDEX IF NOT EXISTS todo_change_store ON todo_change (store, state, seq);

-- A store's snapshot: each task file as the daemon last saw it. fields is the JSON object of the
-- per-field digests a hand edit is reported by.
CREATE TABLE IF NOT EXISTS todo_snapshot (
  store    TEXT NOT NULL,
  path     TEXT NOT NULL,
  id       TEXT NOT NULL,
  rev      TEXT NOT NULL,
  status   TEXT NOT NULL,
  assignee TEXT NOT NULL DEFAULT '',
  fields   TEXT,
  PRIMARY KEY (store, path)
) WITHOUT ROWID;

-- A reader of a store's change log, and the last seq it acknowledged. One silent too long is
-- unregistered and kept, so its return is a resync.
CREATE TABLE IF NOT EXISTS todo_consumer (
  store        TEXT NOT NULL,
  name         TEXT NOT NULL,
  acked_seq    INTEGER NOT NULL DEFAULT 0,
  seen_at      INTEGER,
  unregistered INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (store, name)
) WITHOUT ROWID;
