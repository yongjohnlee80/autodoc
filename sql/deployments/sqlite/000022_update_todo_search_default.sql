-- 000022 — todo tasks are in search by default (ADR 1792015212, amendment 1).
--
-- The workspace stores the opt-out, todo_graph_only, in place of 000021's opt-in, todo_search, so the
-- column's default is the new default. Every workspace starts with its tasks in search: a workspace
-- that turned todo_search on keeps it, and one left at 000021's default moves to the new one.
ALTER TABLE workspace ADD COLUMN todo_graph_only INTEGER NOT NULL DEFAULT 0 CHECK (todo_graph_only IN (0, 1));
ALTER TABLE workspace DROP COLUMN todo_search;
