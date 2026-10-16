-- 000023 — variables belong to the workspace, the KB (ADR 1792132105).
--
-- A todo store belongs to exactly one workspace and reads that workspace's variables. kb is the
-- workspace root the store belongs to, recorded once (the containing workspace, or todos.set_kb);
-- '' until recorded. todo_workspace_var is this machine's values for a workspace's variables, keyed
-- by its root: they never travel, and a KB's portable values are relative paths in its own
-- <root>/.autodoc-vars.toml. todo_store_var (000020) is read once more, by the migration that folds
-- a store's machine values into its workspace's rows.
ALTER TABLE todo_store ADD COLUMN kb TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS todo_workspace_var (
  kb    TEXT NOT NULL,
  name  TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (kb, name)
) WITHOUT ROWID;
