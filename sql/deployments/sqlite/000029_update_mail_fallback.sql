-- 000029 — the fallback (docs/decisions/crew.md#the-fallback-exactly-once): a delivery the crew
-- could not get received in time is withdrawn and handed, once, to its recipient's host as a job,
-- which the host delivers its own way. One job a delivery, ever (UNIQUE (delivery)): a fallback
-- that ran twice would have the host type the message twice.
CREATE TABLE IF NOT EXISTS mail_fallback (
  id          TEXT PRIMARY KEY,
  delivery    TEXT NOT NULL UNIQUE,
  message     TEXT NOT NULL,
  recipient   TEXT NOT NULL,
  -- the recipient's scope: the host holding it carries the job
  host        TEXT NOT NULL,
  -- open (offered) | leased (a host took it) | done | unknown; nothing goes back to open
  state       TEXT NOT NULL,
  lease       TEXT NOT NULL DEFAULT '',
  lease_until INTEGER,
  -- leases taken: a host's retry knows it is one
  tries       INTEGER NOT NULL DEFAULT 0,
  created     INTEGER NOT NULL,
  done_at     INTEGER,
  -- delivered | typed_not_sent | unknown, as the host reported it
  outcome     TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS mail_fallback_host ON mail_fallback(host, state, created);

-- The epoch in which the host last polled for its jobs (mail.fallbacks), set by the write that
-- serves the poll; 0 for one that never has. A delivery falls back only when its scope's host
-- polled in its current epoch: a host that doesn't take jobs (an older build) keeps its mail
-- queued, woken as before, and one restarted takes part again only once it polls.
ALTER TABLE crew_host ADD COLUMN fallbacks INTEGER NOT NULL DEFAULT 0;
