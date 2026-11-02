-- 000031 — who made an assignment (docs/decisions/todos.md#an-assignment-says-who-made-it): the
-- agent whose verified crew session called todos.assign or todos.add, as codename@scope, or the
-- unverified marker, on its change and on the snapshot row of the task it assigned; '' for the
-- user's surface, a legacy assignment, or an assignee changed by anything but an assign. The
-- snapshot's id index covers it too, so a write carrying it forward still reads by the index.
ALTER TABLE todo_change ADD COLUMN assigned_by TEXT NOT NULL DEFAULT '';
ALTER TABLE todo_snapshot ADD COLUMN assigned_by TEXT NOT NULL DEFAULT '';
DROP INDEX IF EXISTS todo_snapshot_id;
CREATE INDEX IF NOT EXISTS todo_snapshot_id ON todo_snapshot (store, id, assignee, scope, assigned_by);
