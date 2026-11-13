-- Undoes 000007_update_writer_lease.sql: the lease table goes. A server of the
-- release before it takes the tenant's advisory lock again.
DROP TABLE IF EXISTS rag_writer_lease;
