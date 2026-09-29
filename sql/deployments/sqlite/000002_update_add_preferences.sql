-- 000002 — preferences: what a client keeps between runs (the TUI's theme,
-- whether its menu bar hides, the side each of its panels docks on).
--
-- The store's, not a workspace's: a preference is the person's, whichever
-- workspace is open. A name is the client's own ("tui.theme"), and so is its
-- value; the store keeps them as written.

CREATE TABLE IF NOT EXISTS preference (
  name       TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
