-- 000020 — a todo store's per-machine variables (ADR 1792015069 §2.3).
--
-- A store's variables resolve the same from every route: its derived built-ins, its own variable
-- file (<store>/.autodoc-vars.toml, which travels with the KB) and, first, this machine's values
-- for the store, kept here, keyed by the store's stable id. A value set here is for a name the
-- store's file declares machine-only, or overrides one on this machine.
CREATE TABLE IF NOT EXISTS todo_store_var (
  store TEXT NOT NULL,
  name  TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (store, name)
) WITHOUT ROWID;
