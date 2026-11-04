-- 000034 on PostgreSQL, the twin of SQLite's — a message is found by its age
-- (docs/decisions/crew.md#handled-mail-is-kept-for-a-window): the retention sweep reads only the
-- messages created before its window, oldest first, a bounded page at a time, by (created, id).
CREATE INDEX IF NOT EXISTS mail_message_created ON mail_message(created, id);
