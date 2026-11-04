-- 000035 revert — the write path's state indexes go. The wake rows of ended sessions it deleted
-- don't come back: nothing reads them.
DROP INDEX IF EXISTS crew_wake_state;
DROP INDEX IF EXISTS mail_delivery_state;
