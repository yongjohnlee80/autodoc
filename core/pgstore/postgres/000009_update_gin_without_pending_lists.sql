-- The document's GIN indexes, on its facets and its tags, keep no pending list.
-- With fastupdate on (PostgreSQL's default) a write adds its entries to an
-- unordered list that every scan of the index reads whole until VACUUM merges
-- it: after a bulk load, a scoped search's facet scan took 3 ms for 37 entries.
-- A document's facets and tags are a few short strings, so writing them into
-- the index directly costs little.
--
-- Setting fastupdate off does not empty a list already there: each is merged
-- here, in the same script.
ALTER INDEX rag_document_facets SET (fastupdate = off);
ALTER INDEX rag_document_tags SET (fastupdate = off);
SELECT gin_clean_pending_list('rag_document_facets'::regclass);
SELECT gin_clean_pending_list('rag_document_tags'::regclass);
