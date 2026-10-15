ALTER TABLE link DROP COLUMN target;
ALTER TABLE workspace DROP COLUMN todo_search;
ALTER TABLE workspace DROP COLUMN graph_only_set;
DROP TABLE IF EXISTS workspace_graph_only;
