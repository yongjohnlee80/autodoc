-- 000035 on PostgreSQL, the twin of SQLite's: the reads every mail wait and scheduler sweep make
-- inside their write search what is due, never the history
-- (docs/decisions/crew.md#the-crew-and-mail-tables): mail_delivery by state, so the expiry,
-- fallback, end-of-session and next-deadline reads find the queued and leased (or failed)
-- deliveries without walking every delivery the store keeps; crew_wake by state, for the awaiting
-- and in-flight rows. An ended session's wake row goes at its end from now on, and the rows ended
-- sessions left before go here, so crew_wake stays the live crew's size.
CREATE INDEX IF NOT EXISTS mail_delivery_state ON mail_delivery(state, recipient, seq);
CREATE INDEX IF NOT EXISTS crew_wake_state ON crew_wake(state, next_at);
DELETE FROM crew_wake WHERE session IN (SELECT id FROM crew_session WHERE ended IS NOT NULL);
