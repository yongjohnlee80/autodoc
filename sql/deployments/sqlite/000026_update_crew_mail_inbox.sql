-- 000026 — the crew registry, durable mail and the human's inbox (ADR 1793271500 §4.2): agents'
-- identity, sessions and wakes; messages with one delivery per recipient, committed together; the
-- feed the human's windows read. The tables belong to no workspace: an agent's scope is a host's
-- (an AutoVim project or an AutoDoc window). Times are Unix nanoseconds, NULL for none, as in 000017.
-- Every state-changing verb runs its guard inside the same write as its change (§4.4).

-- A host holds one scope at an ownership epoch; incarnation is the sha256 of the holding process's
-- run token (never on disk), proc the process it was claimed from.
CREATE TABLE IF NOT EXISTS crew_host (
  id           TEXT PRIMARY KEY,
  epoch        INTEGER NOT NULL,
  incarnation  BLOB NOT NULL DEFAULT x'',
  proc         TEXT NOT NULL DEFAULT '',
  renewed      INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS crew_scope (
  id        TEXT PRIMARY KEY,
  kind      TEXT NOT NULL,
  state     TEXT NOT NULL DEFAULT 'live',
  created   INTEGER NOT NULL
) WITHOUT ROWID;

-- A profile is an identity within a scope (scope, codename); "user" is the human.
CREATE TABLE IF NOT EXISTS crew_profile (
  id          TEXT PRIMARY KEY,
  scope       TEXT NOT NULL,
  codename    TEXT NOT NULL,
  kind        TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'active',
  merged_into TEXT NOT NULL DEFAULT '',
  created     INTEGER NOT NULL
) WITHOUT ROWID;
CREATE UNIQUE INDEX IF NOT EXISTS crew_profile_alias ON crew_profile(scope, codename) WHERE status = 'active';

-- A session is one process of a profile, registered by its host under an epoch; (host, epoch,
-- instance) is the registration's idempotency key, args_digest what it was registered with.
CREATE TABLE IF NOT EXISTS crew_session (
  id          TEXT PRIMARY KEY,
  profile     TEXT NOT NULL,
  host        TEXT NOT NULL,
  epoch       INTEGER NOT NULL,
  instance    TEXT NOT NULL,
  secret_hash BLOB NOT NULL,
  args_digest BLOB NOT NULL,
  started     INTEGER NOT NULL,
  ended       INTEGER,
  ended_why   TEXT NOT NULL DEFAULT '',
  UNIQUE (host, epoch, instance)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS crew_session_live ON crew_session(profile) WHERE ended IS NULL;

CREATE TABLE IF NOT EXISTS crew_endpoint (
  session     TEXT PRIMARY KEY,
  generation  INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  locator     TEXT NOT NULL,
  registered  INTEGER NOT NULL
) WITHOUT ROWID;

-- The wake scheduler's state machine, one row a session (§4.7).
CREATE TABLE IF NOT EXISTS crew_wake (
  session       TEXT PRIMARY KEY,
  pending_gen   INTEGER NOT NULL DEFAULT 0,
  announced_gen INTEGER NOT NULL DEFAULT 0,
  state         TEXT NOT NULL DEFAULT 'idle',
  token         TEXT NOT NULL DEFAULT '',
  attempt_gen   INTEGER NOT NULL DEFAULT 0,
  failures      INTEGER NOT NULL DEFAULT 0,
  daemon_run    TEXT NOT NULL DEFAULT '',
  next_at       INTEGER
) WITHOUT ROWID;

-- A message, sent by an authenticated session's profile (a generated event's sender is the
-- reserved crew:autodoc); (sender, idem_key) makes a send idempotent.
CREATE TABLE IF NOT EXISTS mail_message (
  id             TEXT PRIMARY KEY,
  sender         TEXT NOT NULL,
  sender_session TEXT NOT NULL DEFAULT '',
  idem_key       TEXT NOT NULL,
  digest         BLOB NOT NULL,
  kind           TEXT NOT NULL,
  subject        TEXT NOT NULL DEFAULT '',
  body           TEXT NOT NULL,
  reply_to       TEXT NOT NULL DEFAULT '',
  state          TEXT NOT NULL DEFAULT 'open',
  created        INTEGER NOT NULL,
  expires        INTEGER,
  UNIQUE (sender, idem_key)
) WITHOUT ROWID;
CREATE UNIQUE INDEX IF NOT EXISTS mail_one_reply ON mail_message(reply_to, sender) WHERE kind = 'response';

-- A recipient's sequence high-water: never reused, never pruned.
CREATE TABLE IF NOT EXISTS mail_recipient (
  profile    TEXT PRIMARY KEY,
  next_seq   INTEGER NOT NULL
) WITHOUT ROWID;

-- One delivery of a message to one recipient, bound to a session or not ('').
CREATE TABLE IF NOT EXISTS mail_delivery (
  id          TEXT PRIMARY KEY,
  message     TEXT NOT NULL,
  recipient   TEXT NOT NULL,
  session     TEXT NOT NULL DEFAULT '',
  seq         INTEGER NOT NULL,
  state       TEXT NOT NULL,
  lease       TEXT NOT NULL DEFAULT '',
  lease_until INTEGER,
  attempts    INTEGER NOT NULL DEFAULT 0,
  read_at     INTEGER,
  done_at     INTEGER,
  why         TEXT NOT NULL DEFAULT '',
  UNIQUE (recipient, seq),
  UNIQUE (message, recipient)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS mail_delivery_open ON mail_delivery(recipient, state, seq);
CREATE INDEX IF NOT EXISTS mail_delivery_deadline ON mail_delivery(lease_until) WHERE state = 'leased';

-- A permission prompt or a block an agent's adapter reported, shown to the human.
CREATE TABLE IF NOT EXISTS crew_attention (
  session    TEXT PRIMARY KEY,
  owner      TEXT NOT NULL,
  kind       TEXT NOT NULL,
  text       TEXT NOT NULL,
  raised     INTEGER NOT NULL,
  cleared    INTEGER
) WITHOUT ROWID;

-- The human feed's journal (§4.8): every window reads it from a cursor.
CREATE TABLE IF NOT EXISTS inbox_change (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL,
  ref        TEXT NOT NULL,
  at         INTEGER NOT NULL
);
