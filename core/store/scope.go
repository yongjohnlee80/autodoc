package store

import (
	"context"

	"github.com/yongjohnlee80/golib/dao"
)

// Scope is one workspace's tables. Every accessor returns a DAO that carries
// the workspace by a dao hook, per statement: an insert or upsert has its
// workspace_id staged, and every other statement (a read, an update, a
// delete) has workspace_id = the workspace added to its WHERE. Code outside
// this package cannot name a workspace-owned table any other way. A join from
// one of them to another stays inside the workspace by the schema's composite
// foreign keys, (workspace_id, id).
//
// An update never sets workspace_id: the column is part of the key other
// tables reference, and setting it, even to its own value, makes SQLite check
// every one of them.
type Scope struct {
	id   int64
	hook dao.QueryOption
}

// scopeHook puts the workspace on each statement, by what the statement is.
type scopeHook struct {
	dao.NopHook
	ws int64
}

func (h scopeHook) BeforeBuild(_ context.Context, q *dao.QueryInfo, s dao.Stager) error {
	switch q.Op {
	case dao.OpInsert, dao.OpUpsert:
		s.SetColumn(wsCol, h.ws)
	case dao.OpBatch, dao.OpBatchCopy:
		// a Batch stamps each row it adds
	default:
		s.Where(dao.Eq(`"`+q.Table+`"."`+wsCol+`"`, h.ws))
	}
	return nil
}

// Workspace is the scope of the workspace id.
func (s *Store) Workspace(id int64) *Scope {
	return &Scope{id: id, hook: dao.WithHooks(scopeHook{ws: id})}
}

// ID is the scope's workspace.
func (sc *Scope) ID() int64 { return sc.id }

func scoped[R any, C ~string, K ~string, ID any](sc *Scope, s *dao.Schema[R, C, K, ID], tx *Tx) dao.DAO[R, C, ID] {
	return s.On(tx.tx, sc.hook)
}

// Self is the workspace's own row, for its counters.
func (sc *Scope) Self(tx *Tx) dao.DAO[*Workspace, WorkspaceField, int64] {
	return tx.t.workspaces.On(tx.tx).With(WorkspaceID, sc.id)
}

func (sc *Scope) Patterns(tx *Tx) dao.DAO[*Pattern, PatternField, int64] {
	return scoped(sc, tx.t.patterns, tx)
}
func (sc *Scope) Documents(tx *Tx) dao.DAO[*Document, DocumentField, int64] {
	return scoped(sc, tx.t.documents, tx)
}
func (sc *Scope) Chunks(tx *Tx) dao.DAO[*Chunk, ChunkField, int64] {
	return scoped(sc, tx.t.chunks, tx)
}
func (sc *Scope) Tags(tx *Tx) dao.DAO[*DocValue, DocValueField, int64] {
	return scoped(sc, tx.t.tags, tx)
}
func (sc *Scope) Aliases(tx *Tx) dao.DAO[*DocValue, DocValueField, int64] {
	return scoped(sc, tx.t.aliases, tx)
}
func (sc *Scope) Names(tx *Tx) dao.DAO[*DocName, DocNameField, int64] {
	return scoped(sc, tx.t.names, tx)
}

// LinksOut is the link table joined to each link's target; LinksIn to each
// link's source. Both are the one link table.
func (sc *Scope) LinksOut(tx *Tx) dao.DAO[*Link, LinkField, int64] {
	return scoped(sc, tx.t.linksOut, tx)
}
func (sc *Scope) LinksIn(tx *Tx) dao.DAO[*Link, LinkField, int64] {
	return scoped(sc, tx.t.linksIn, tx)
}
func (sc *Scope) Models(tx *Tx) dao.DAO[*Model, ModelField, string] {
	return scoped(sc, tx.t.models, tx)
}
func (sc *Scope) Embeddings(tx *Tx) dao.DAO[*Embedding, EmbeddingField, string] {
	return scoped(sc, tx.t.embeddings, tx)
}
func (sc *Scope) Jobs(tx *Tx) dao.DAO[*Job, JobField, string] { return scoped(sc, tx.t.jobs, tx) }
func (sc *Scope) Changes(tx *Tx) dao.DAO[*Change, ChangeField, int64] {
	return scoped(sc, tx.t.changes, tx)
}

// Batch is a batch insert into one of the workspace's tables: every row added
// is stamped with the workspace.
type Batch[R any, C ~string] struct {
	b  dao.BatchWriter[R, C]
	ws int64
}

// Add stages one row.
func (b Batch[R, C]) Add(values map[C]any) Batch[R, C] {
	values[C(wsCol)] = b.ws
	b.b.Add(values)
	return b
}

// SkipConflicts keeps a row that already exists (ON CONFLICT DO NOTHING).
func (b Batch[R, C]) SkipConflicts() Batch[R, C] {
	b.b.SkipConflicts()
	return b
}

// Flush writes the staged rows.
func (b Batch[R, C]) Flush() error { return b.b.Flush() }

func batch[R any, C ~string, K ~string, ID any](sc *Scope, s *dao.Schema[R, C, K, ID], tx *Tx) Batch[R, C] {
	return Batch[R, C]{b: s.On(tx.tx).Batch(), ws: sc.id}
}

func (sc *Scope) TagBatch(tx *Tx) Batch[*DocValue, DocValueField] { return batch(sc, tx.t.tags, tx) }
func (sc *Scope) AliasBatch(tx *Tx) Batch[*DocValue, DocValueField] {
	return batch(sc, tx.t.aliases, tx)
}
func (sc *Scope) NameBatch(tx *Tx) Batch[*DocName, DocNameField] { return batch(sc, tx.t.names, tx) }
func (sc *Scope) EmbeddingBatch(tx *Tx) Batch[*Embedding, EmbeddingField] {
	return batch(sc, tx.t.embeddings, tx)
}
func (sc *Scope) PatternBatch(tx *Tx) Batch[*Pattern, PatternField] {
	return batch(sc, tx.t.patterns, tx)
}
