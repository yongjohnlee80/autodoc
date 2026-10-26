-- 000024 — a todo store's release (ADR 1791532020 r13): when todos.release gave it back, NULL for
-- never. The daemon claims a store at its first write, unless it was released: a released store is
-- claimed again only by todos.claim.
ALTER TABLE todo_store ADD COLUMN released_at INTEGER;
