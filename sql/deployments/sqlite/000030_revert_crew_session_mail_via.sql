-- 000030 revert — a crew session no longer says how it reads its mail.
ALTER TABLE crew_session DROP COLUMN mail_via;
