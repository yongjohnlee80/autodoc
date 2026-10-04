DROP TABLE workspace_connection;
ALTER TABLE workspace DROP COLUMN view_args;
ALTER TABLE workspace DROP COLUMN vector_index;
ALTER TABLE workspace DROP COLUMN destination;
DROP INDEX workspace_uid;
ALTER TABLE workspace DROP COLUMN uid;
