-- 000025 — an assignment's scope (ADR 1791532020 r15 §2.5a): the opaque scope todos.assign was
-- given, on its change and on the snapshot row of the task it assigned. '' for none: a legacy
-- assignment, or an assignee changed by anything but an assign. A write that carries a scope
-- forward reads a task's row by id; the index covers that read (assignee, scope), so the planner,
-- with no statistics, takes it over the primary key's store prefix.
ALTER TABLE todo_change ADD COLUMN scope TEXT NOT NULL DEFAULT '';
ALTER TABLE todo_snapshot ADD COLUMN scope TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS todo_snapshot_id ON todo_snapshot (store, id, assignee, scope);
