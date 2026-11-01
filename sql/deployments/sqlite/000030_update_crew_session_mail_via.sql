-- 000030 — a crew session says how it reads its mail (docs/decisions/crew.md#the-crew-mcp-server):
-- crew.register's mail_via, cli or mcp, as the session's setup delivered it, which its wake notice
-- and its guide follow. '' for a session registered without it, and for one registered before this
-- script: its mail is read as its kind's is (crew.MailViaOf).
ALTER TABLE crew_session ADD COLUMN mail_via TEXT NOT NULL DEFAULT '';
