-- 000034 revert — messages are no longer indexed by their age.
DROP INDEX IF EXISTS mail_message_created;
