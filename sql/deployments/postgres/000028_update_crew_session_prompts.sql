-- 000028 on PostgreSQL, the twin of SQLite's: a crew session says whether its permission prompts
-- are reported (docs/decisions/crew.md#the-human-inbox): crew.register's prompts, 1 when the host
-- wrote the hooks that report them, which crew.list's prompts_reported then trusts while the
-- session is live. 0 for a session registered without it, and for one registered before this
-- script: its prompts are never assumed reported.
ALTER TABLE crew_session ADD COLUMN prompts bigint NOT NULL DEFAULT 0;
