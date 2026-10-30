-- 000026 on PostgreSQL — the crew registry, durable mail and the human's inbox (ADR 1793271500 §4.2): agents'
-- identity, sessions and wakes; messages with one delivery per recipient, committed together; the
-- feed the human's windows read. The tables belong to no workspace: an agent's scope is a host's
-- (an AutoVim project or an AutoDoc window). Times are Unix nanoseconds, NULL for none, as in 000017.
-- Every state-changing verb runs its guard inside the same write as its change (§4.4).
-- Translated as postgres/000001 is (ADR 1793319634): SQLite's INTEGER is bigint, BLOB bytea, TEXT text COLLATE "C",
-- AUTOINCREMENT a bigint identity, and WITHOUT ROWID a plain table with the same key.

-- A host holds one scope at an ownership epoch; incarnation is the sha256 of the holding process's
-- run token (never on disk), proc the process it was claimed from.
CREATE TABLE IF NOT EXISTS crew_host (
  id           text COLLATE "C" PRIMARY KEY,
  epoch        bigint NOT NULL,
  incarnation  bytea NOT NULL DEFAULT '\x'::bytea,
  proc         text COLLATE "C" NOT NULL DEFAULT '',
  renewed      bigint NOT NULL
);

CREATE TABLE IF NOT EXISTS crew_scope (
  id        text COLLATE "C" PRIMARY KEY,
  kind      text COLLATE "C" NOT NULL,
  state     text COLLATE "C" NOT NULL DEFAULT 'live',
  created   bigint NOT NULL
);

-- A profile is an identity within a scope (scope, codename); "user" is the human.
CREATE TABLE IF NOT EXISTS crew_profile (
  id          text COLLATE "C" PRIMARY KEY,
  scope       text COLLATE "C" NOT NULL,
  codename    text COLLATE "C" NOT NULL,
  kind        text COLLATE "C" NOT NULL DEFAULT '',
  status      text COLLATE "C" NOT NULL DEFAULT 'active',
  merged_into text COLLATE "C" NOT NULL DEFAULT '',
  created     bigint NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS crew_profile_alias ON crew_profile(scope, codename) WHERE status = 'active';

-- A session is one process of a profile, registered by its host under an epoch; (host, epoch,
-- instance) is the registration's idempotency key, args_digest what it was registered with.
CREATE TABLE IF NOT EXISTS crew_session (
  id          text COLLATE "C" PRIMARY KEY,
  profile     text COLLATE "C" NOT NULL,
  host        text COLLATE "C" NOT NULL,
  epoch       bigint NOT NULL,
  instance    text COLLATE "C" NOT NULL,
  secret_hash bytea NOT NULL,
  args_digest bytea NOT NULL,
  started     bigint NOT NULL,
  ended       bigint,
  ended_why   text COLLATE "C" NOT NULL DEFAULT '',
  UNIQUE (host, epoch, instance)
);
CREATE INDEX IF NOT EXISTS crew_session_live ON crew_session(profile) WHERE ended IS NULL;

CREATE TABLE IF NOT EXISTS crew_endpoint (
  session     text COLLATE "C" PRIMARY KEY,
  generation  bigint NOT NULL,
  kind        text COLLATE "C" NOT NULL,
  locator     text COLLATE "C" NOT NULL,
  registered  bigint NOT NULL
);

-- The wake scheduler's state machine, one row a session (§4.7).
CREATE TABLE IF NOT EXISTS crew_wake (
  session       text COLLATE "C" PRIMARY KEY,
  pending_gen   bigint NOT NULL DEFAULT 0,
  announced_gen bigint NOT NULL DEFAULT 0,
  state         text COLLATE "C" NOT NULL DEFAULT 'idle',
  token         text COLLATE "C" NOT NULL DEFAULT '',
  attempt_gen   bigint NOT NULL DEFAULT 0,
  failures      bigint NOT NULL DEFAULT 0,
  daemon_run    text COLLATE "C" NOT NULL DEFAULT '',
  next_at       bigint
);

-- A message, sent by an authenticated session's profile (a generated event's sender is the
-- reserved crew:autodoc); (sender, idem_key) makes a send idempotent.
CREATE TABLE IF NOT EXISTS mail_message (
  id             text COLLATE "C" PRIMARY KEY,
  sender         text COLLATE "C" NOT NULL,
  sender_session text COLLATE "C" NOT NULL DEFAULT '',
  idem_key       text COLLATE "C" NOT NULL,
  digest         bytea NOT NULL,
  kind           text COLLATE "C" NOT NULL,
  subject        text COLLATE "C" NOT NULL DEFAULT '',
  body           text COLLATE "C" NOT NULL,
  reply_to       text COLLATE "C" NOT NULL DEFAULT '',
  state          text COLLATE "C" NOT NULL DEFAULT 'open',
  created        bigint NOT NULL,
  expires        bigint,
  UNIQUE (sender, idem_key)
);
CREATE UNIQUE INDEX IF NOT EXISTS mail_one_reply ON mail_message(reply_to, sender) WHERE kind = 'response';

-- A recipient's sequence high-water: never reused, never pruned.
CREATE TABLE IF NOT EXISTS mail_recipient (
  profile    text COLLATE "C" PRIMARY KEY,
  next_seq   bigint NOT NULL
);

-- One delivery of a message to one recipient, bound to a session or not ('').
CREATE TABLE IF NOT EXISTS mail_delivery (
  id          text COLLATE "C" PRIMARY KEY,
  message     text COLLATE "C" NOT NULL,
  recipient   text COLLATE "C" NOT NULL,
  session     text COLLATE "C" NOT NULL DEFAULT '',
  seq         bigint NOT NULL,
  state       text COLLATE "C" NOT NULL,
  lease       text COLLATE "C" NOT NULL DEFAULT '',
  lease_until bigint,
  attempts    bigint NOT NULL DEFAULT 0,
  read_at     bigint,
  done_at     bigint,
  why         text COLLATE "C" NOT NULL DEFAULT '',
  UNIQUE (recipient, seq),
  UNIQUE (message, recipient)
);
CREATE INDEX IF NOT EXISTS mail_delivery_open ON mail_delivery(recipient, state, seq);
CREATE INDEX IF NOT EXISTS mail_delivery_deadline ON mail_delivery(lease_until) WHERE state = 'leased';

-- A permission prompt or a block an agent's adapter reported, shown to the human.
CREATE TABLE IF NOT EXISTS crew_attention (
  session    text COLLATE "C" PRIMARY KEY,
  owner      text COLLATE "C" NOT NULL,
  kind       text COLLATE "C" NOT NULL,
  text       text COLLATE "C" NOT NULL,
  raised     bigint NOT NULL,
  cleared    bigint
);

-- The human feed's journal (§4.8): every window reads it from a cursor.
CREATE TABLE IF NOT EXISTS inbox_change (
  seq        bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  kind       text COLLATE "C" NOT NULL,
  ref        text COLLATE "C" NOT NULL,
  at         bigint NOT NULL
);
