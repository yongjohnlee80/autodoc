-- 000018 — a todo consumer's resync, delivered at least once (ADR 1791532020 §2.5).
--
-- A consumer unregistered for its silence is answered a resync: the store's snapshot and a cursor
-- at the head. Its acked_seq stays where it acknowledged, and it stays unregistered, answered the
-- resync again on every ask, until it acknowledges the resync's cursor. resync_seq is that cursor,
-- of the resync last answered (seen_at says when): an ack below it is from before the silence, and
-- is refused. 0 for a consumer answered none.
ALTER TABLE todo_consumer ADD COLUMN resync_seq INTEGER NOT NULL DEFAULT 0;
