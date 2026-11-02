-- 000032 — a crew endpoint keeps when its first generation registered
-- (docs/decisions/crew.md#the-fallback-exactly-once): a Claude session's endpoint registers once
-- Claude Code is past its startup, and its mail's fallback window opens then; later generations
-- (a resume, a compaction) keep it. 0 for an endpoint registered before this script: its window
-- opens at its session's start, as before.
ALTER TABLE crew_endpoint ADD COLUMN first_registered INTEGER NOT NULL DEFAULT 0;
