-- 000025 revert — an assignment has no scope again.
DROP INDEX IF EXISTS todo_snapshot_id;
ALTER TABLE todo_snapshot DROP COLUMN scope;
ALTER TABLE todo_change DROP COLUMN scope;
