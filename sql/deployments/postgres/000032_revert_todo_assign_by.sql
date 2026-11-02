-- 000032 revert on PostgreSQL — an assignment no longer says who made it.
DROP INDEX IF EXISTS todo_snapshot_id;
ALTER TABLE todo_snapshot DROP COLUMN assigned_by;
ALTER TABLE todo_change DROP COLUMN assigned_by;
CREATE INDEX todo_snapshot_id ON todo_snapshot (store, id, assignee, scope);
