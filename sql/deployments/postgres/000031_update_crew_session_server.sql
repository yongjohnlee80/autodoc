-- 000031 — a Codex session keeps its own app-server, as its endpoint named it when it registered
-- (docs/decisions/crew.md#crew-bridges): the server's socket and its process reference (ProcRef).
-- Kept after the session ends, they prove a server still running to be this store's, so the
-- daemon's reclaim can end it once orphaned. '' for every other session, and for one registered
-- before this script.
ALTER TABLE crew_session ADD COLUMN server text NOT NULL DEFAULT '';
ALTER TABLE crew_session ADD COLUMN server_ref text NOT NULL DEFAULT '';
