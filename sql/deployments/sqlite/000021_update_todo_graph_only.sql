-- 000021 — todo stores in the graph, not in search (ADR 1792015212).
--
-- A workspace's graph_only patterns: paths indexed for their row, frontmatter, links and relations,
-- and no chunks. graph_only_set says the list was set; before it is, the workspace has the default
-- (**/.todo-list/**). todo_search returns the workspace's todo tasks to full indexing.
CREATE TABLE IF NOT EXISTS workspace_graph_only (
  workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  ord          INTEGER NOT NULL,
  pattern      TEXT NOT NULL,
  PRIMARY KEY (workspace_id, ord)
) WITHOUT ROWID;
ALTER TABLE workspace ADD COLUMN graph_only_set INTEGER NOT NULL DEFAULT 0 CHECK (graph_only_set IN (0, 1));
ALTER TABLE workspace ADD COLUMN todo_search INTEGER NOT NULL DEFAULT 0 CHECK (todo_search IN (0, 1));
-- An external link's file, absolute: a todo task's ref outside the workspace. It reaches no document.
ALTER TABLE link ADD COLUMN target TEXT;
