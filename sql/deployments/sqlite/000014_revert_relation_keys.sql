ALTER TABLE workspace DROP COLUMN demote_superseded;
ALTER TABLE workspace DROP COLUMN abstract_chunk;
ALTER TABLE chunk DROP COLUMN kind;
DROP INDEX IF EXISTS link_key_key;
DROP TABLE IF EXISTS link_key;
