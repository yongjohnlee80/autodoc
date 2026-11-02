-- 000032 on PostgreSQL, the twin of SQLite's: who made an assignment
-- (docs/decisions/todos.md#an-assignment-says-who-made-it), codename@scope or the unverified
-- marker, on its change and its task's snapshot row; '' for the user's surface, a legacy
-- assignment, or an assignee changed by anything but an assign. The snapshot's id index covers it.
ALTER TABLE todo_change ADD COLUMN assigned_by text COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE todo_snapshot ADD COLUMN assigned_by text COLLATE "C" NOT NULL DEFAULT '';
DROP INDEX IF EXISTS todo_snapshot_id;
CREATE INDEX todo_snapshot_id ON todo_snapshot (store, id, assignee, scope, assigned_by);
