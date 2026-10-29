package store

import (
	"fmt"
	"sync"

	"github.com/yongjohnlee80/golib/dao"
)

// DECLARED TABLES — a package's own tables in the store. The tables themselves are the migrations'
// (sql/deployments); a declaration is their Go side: the package declares them at its init, and
// every store builds every declaration when it opens, once for the writer's connection and once for
// the readers'. The list is fixed at link time: a declaration after a store has opened, or a second
// of one name, panics, so no test can change it under another.

// declarations are every package's, in the order declared.
var declarations struct {
	mu     sync.Mutex
	frozen bool // a store has opened
	names  map[string]bool
	builds []func(dao.DataConn) any
}

// Declared is a package's tables in the store, as T.
type Declared[T any] struct{ i int }

// Declare declares a package's tables, named for its refusals ("todo"): build makes them on a
// connection. Call it from a package-level var, so it runs at init.
func Declare[T any](name string, build func(c dao.DataConn) T) Declared[T] {
	declarations.mu.Lock()
	defer declarations.mu.Unlock()
	switch {
	case declarations.frozen:
		panic(fmt.Sprintf("store: the tables %q are declared after a store opened", name))
	case declarations.names[name]:
		panic(fmt.Sprintf("store: the tables %q are declared twice", name))
	}
	if declarations.names == nil {
		declarations.names = map[string]bool{}
	}
	declarations.names[name] = true
	declarations.builds = append(declarations.builds, func(c dao.DataConn) any { return build(c) })
	return Declared[T]{i: len(declarations.builds) - 1}
}

// In are the tables for tx's connection.
func (d Declared[T]) In(tx *Tx) T { return tx.t.declared[d.i].(T) }

// built are every declaration built on c, freezing the list.
func built(c dao.DataConn) []any {
	declarations.mu.Lock()
	defer declarations.mu.Unlock()
	declarations.frozen = true
	out := make([]any, len(declarations.builds))
	for i, b := range declarations.builds {
		out[i] = b(c)
	}
	return out
}

// Transaction is the dao transaction tx is, for a package's own declared tables. A workspace's
// tables are reached through its Scope (Scoped), which stamps every statement with the workspace.
func (tx *Tx) Transaction() *dao.Transaction { return tx.tx }

// On is s's DAO inside tx, unscoped: for a package's own tables that no workspace owns.
func On[R any, C ~string, K ~string, ID any](s *dao.Schema[R, C, K, ID], tx *Tx) dao.DAO[R, C, ID] {
	return s.On(tx.tx)
}

// Scoped is s's DAO inside tx, scoped to sc's workspace: every statement carries it.
func Scoped[R any, C ~string, K ~string, ID any](sc *Scope, s *dao.Schema[R, C, K, ID], tx *Tx) dao.DAO[R, C, ID] {
	return scoped(sc, s, tx)
}

// NewBatch is a batch insert into s inside tx, every row stamped with sc's workspace.
func NewBatch[R any, C ~string, K ~string, ID any](sc *Scope, s *dao.Schema[R, C, K, ID], tx *Tx) Batch[R, C] {
	return batch(sc, s, tx)
}

// Col is a column of table, read into a row by scan.
func Col[R any, T ~string](table string, c T, scan func(R) any) dao.Field[R] {
	return col(table, c, scan)
}

// Joined is a read-only column of table, reached through the join join.
func Joined[R any, T ~string](table string, c T, join dao.JoinKey, scan func(R) any) dao.Field[R] {
	return joined(table, c, join, scan)
}

// WorkspaceColumn is the column every workspace's table keys on first.
const WorkspaceColumn = wsCol

// InnerJoin joins table to from on the workspace, then table's col equal to from's key: a join
// between two workspace-owned tables is on both. LeftJoin keeps a from row that joins none.
func InnerJoin(table, col, from, key string) dao.Expr { return innerJoin(table, col, from, key) }

func LeftJoin(table, col, from, key string) dao.Expr { return leftJoin(table, col, from, key) }

// NoMatchIn is the predicate of a row of from that table has no row for: none on the workspace
// whose col equals from's key and whose eqCol is eqVal. It is a correlated NOT EXISTS, so table is
// probed by its key for each row of from read and never read whole.
func NoMatchIn(table, col, from, key, eqCol string, eqVal any) dao.Predicate {
	return noMatchIn(table, col, from, key, eqCol, eqVal)
}

// NoText is the empty string as a column expression: what a COALESCE over a join that found no
// row falls back to.
var NoText = dao.SQL("''")
