-- 000029 revert on PostgreSQL — the fallback's jobs go, with every job in them.
DROP INDEX IF EXISTS mail_fallback_host;
DROP TABLE IF EXISTS mail_fallback;
ALTER TABLE crew_host DROP COLUMN fallbacks;
