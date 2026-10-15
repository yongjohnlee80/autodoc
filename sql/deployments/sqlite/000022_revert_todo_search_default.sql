ALTER TABLE workspace ADD COLUMN todo_search INTEGER NOT NULL DEFAULT 0 CHECK (todo_search IN (0, 1));
UPDATE workspace SET todo_search = 1 - todo_graph_only;
ALTER TABLE workspace DROP COLUMN todo_graph_only;
