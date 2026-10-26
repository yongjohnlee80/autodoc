-- 000024 revert — the release goes: a store's claim is its claimed flag alone again.
ALTER TABLE todo_store DROP COLUMN released_at;
