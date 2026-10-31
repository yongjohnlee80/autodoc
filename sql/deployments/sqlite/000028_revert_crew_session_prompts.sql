-- 000028 revert — a crew session no longer says whether its permission prompts are reported.
ALTER TABLE crew_session DROP COLUMN prompts;
