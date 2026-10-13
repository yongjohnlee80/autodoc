-- 000019 — a todo store's daily refresh (ADR 1791532020 §2.3).
--
-- The daemon refreshes each claimed store once a day by itself: bucket placement, errors[], and
-- the 28-day archive. refreshed_at is when that refresh last ran on the store (Unix nanoseconds,
-- NULL for never), so a daemon that starts runs it at once only when the last run is a day old,
-- and a restart neither repeats nor skips a day. A run that failed counts; it is reported.
ALTER TABLE todo_store ADD COLUMN refreshed_at INTEGER;
