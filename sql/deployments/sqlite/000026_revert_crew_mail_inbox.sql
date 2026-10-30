-- 000026 revert — the crew registry, mail and the inbox go, with every message in them.
DROP TABLE IF EXISTS inbox_change;
DROP TABLE IF EXISTS crew_attention;
DROP INDEX IF EXISTS mail_delivery_deadline;
DROP INDEX IF EXISTS mail_delivery_open;
DROP TABLE IF EXISTS mail_delivery;
DROP TABLE IF EXISTS mail_recipient;
DROP INDEX IF EXISTS mail_one_reply;
DROP TABLE IF EXISTS mail_message;
DROP TABLE IF EXISTS crew_wake;
DROP TABLE IF EXISTS crew_endpoint;
DROP INDEX IF EXISTS crew_session_live;
DROP TABLE IF EXISTS crew_session;
DROP INDEX IF EXISTS crew_profile_alias;
DROP TABLE IF EXISTS crew_profile;
DROP TABLE IF EXISTS crew_scope;
DROP TABLE IF EXISTS crew_host;
