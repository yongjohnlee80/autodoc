// Package pgstore is a tenant-scoped PostgreSQL + pgvector index behind golib's search port: a
// search.Store[int64, *View] whose views are Semantic, Signaler and Lister, built on golib's dao
// alone (no SQL of its own beyond its migrations).
//
// Every table's key starts with the tenant, every join between them carries it, every upsert
// conflicts on a tenant-qualified key, and every statement, reads and writes alike, is filtered by
// the Store's tenant. Embeddings are keyed by (tenant, model, text hash), so tenants never share a
// vector and a switch of one tenant's model leaves another's untouched.
//
// Each View is one REPEATABLE READ, READ ONLY transaction, so every retriever and presentation field
// it answers reads one snapshot. Lexical search matches a stored "simple" tsvector (breadcrumb
// class A, body class D) and ranks with ts_rank_cd against the bound query; semantic search scans
// exactly, ordered by cosine distance to the bound vector over documents ready under the active
// model, and answers search.ErrModelChanged for any other model. Filters sit in the same WHERE as
// the match, so they apply before the rank and the limit; ties fall to path order (byte order)
// and then the ordinal.
//
// Migrate applies the schema through dao/deploy, recording it in its own ledger table.
package pgstore
