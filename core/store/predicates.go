package store

import (
	"strings"

	"github.com/yongjohnlee80/golib/dao"
)

// PREDICATES THE DAO HAS NONE FOR — SQL text is the store's (source_test.go), so a package that
// needs a predicate dao lacks asks for it here. Each renders its own placeholders through the
// dialect, numbered after the statement's others, as dao's own predicates do.

// RowAfter is the predicate of a row whose columns, compared in order, come after vals: the
// row-value comparison (c1, c2, …) > (v1, v2, …), which seeks an index on those columns as a range.
// One column is col > v. cols are column expressions, quoted as dao's predicates take them.
func RowAfter(cols []string, vals ...any) dao.Predicate { return &rowAfter{cols: cols, vals: vals} }

type rowAfter struct {
	cols []string
	vals []any
}

func (p *rowAfter) ToSQL(d dao.Dialect, next *int) (string, []any) {
	ph := make([]string, len(p.vals))
	for i := range p.vals {
		*next++
		ph[i] = d.Placeholder(*next)
	}
	if len(p.cols) == 1 {
		return p.cols[0] + " > " + ph[0], append([]any(nil), p.vals...)
	}
	return "(" + strings.Join(p.cols, ", ") + ") > (" + strings.Join(ph, ", ") + ")", append([]any(nil), p.vals...)
}

// MatchIn is the predicate of a row of from that table, a workspace's table, has a row for: one on
// from's workspace whose col equals from's key, and that every one of where admits. It is
// Correlated, with the workspace's equality first: a correlated EXISTS that probes table by its
// (workspace_id, col) key for each row of from read, never reading it whole. where's columns are
// table's, quoted as dao's predicates take them ("doc_tag"."tag").
func MatchIn(table, col, from, key string, where ...dao.Predicate) dao.Predicate {
	ws := dao.Cmp(dao.T(table, wsCol), dao.OpEq, dao.T(from, wsCol))
	return Correlated(false, table, col, from, key, append([]dao.Predicate{ws}, where...)...)
}
