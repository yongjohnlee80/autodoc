-- 000032 revert — an assignment no longer says who made it.
DROP INDEX IF EXISTS todo_snapshot_id;
ALTER TABLE todo_snapshot DROP COLUMN assigned_by;
ALTER TABLE todo_change DROP COLUMN assigned_by;
CREATE INDEX IF NOT EXISTS todo_snapshot_id ON todo_snapshot (store, id, assignee, scope);
