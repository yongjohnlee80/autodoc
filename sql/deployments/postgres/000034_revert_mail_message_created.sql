-- 000034 revert on PostgreSQL — messages are no longer indexed by their age.
DROP INDEX IF EXISTS mail_message_created;
