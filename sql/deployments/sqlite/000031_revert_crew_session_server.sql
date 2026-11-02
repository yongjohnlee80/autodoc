-- 000031 reverted: a session no longer keeps its Codex server's socket and process.
ALTER TABLE crew_session DROP COLUMN server_ref;
ALTER TABLE crew_session DROP COLUMN server;
