-- 000009 — configuration and lifecycle events (ADR 0212 §7).
--
-- One daemon-wide, bounded log a client follows by cursor (sys.events), so a
-- change one client makes (a model switch, a workspace's patterns or schema)
-- reaches the others. seq never repeats (AUTOINCREMENT), so a cursor stays
-- meaningful after old rows are pruned. A row names what changed, the
-- workspace it is about ('' for none), the client that made it ('' for the
-- daemon itself), and a short detail (a model's or a new name); never a
-- secret, never a note's content.
CREATE TABLE IF NOT EXISTS event (
  seq       INTEGER PRIMARY KEY AUTOINCREMENT,
  kind      TEXT NOT NULL,
  workspace TEXT NOT NULL DEFAULT '',
  client    TEXT NOT NULL DEFAULT '',
  detail    TEXT NOT NULL DEFAULT '',
  at        INTEGER NOT NULL
);
