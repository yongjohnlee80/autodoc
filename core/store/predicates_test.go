package store

import (
	"reflect"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
)

// positional is a dialect whose placeholders are all "?" (SQLite's, MySQL's); GenericDialect's are
// numbered "$n" (PostgreSQL's).
type positional struct{ dao.GenericDialect }

func (positional) Placeholder(int) string { return "?" }

// RowAfter and MatchIn render their placeholders through the dialect, numbered after the
// statement's others, and their arguments in that order: a row value, one column, and an EXISTS
// on the workspace and the key with conditions of dao's own.
func TestThePredicatesRenderForEitherDialect(t *testing.T) {
	cols := []string{`"document"."k_upd_s"`, `"document"."k_upd_ns"`, `"document"."path"`}
	in := MatchIn("doc_facet", "doc_id", "document", "id", dao.Eq(`"doc_facet"."field"`, "status"),
		dao.In(`"doc_facet"."value"`, []any{"a", "b"}))
	for _, c := range []struct {
		name string
		d    dao.Dialect
		p    dao.Predicate
		from int
		sql  string
		args []any
	}{
		{"a row value, numbered", dao.GenericDialect{}, RowAfter(cols, int64(-5), int64(0), "a.md"), 2,
			`("document"."k_upd_s", "document"."k_upd_ns", "document"."path") > ($3, $4, $5)`, []any{int64(-5), int64(0), "a.md"}},
		{"a row value, positional", positional{}, RowAfter(cols[1:], int64(0), "a.md"), 0,
			`("document"."k_upd_ns", "document"."path") > (?, ?)`, []any{int64(0), "a.md"}},
		{"one column", dao.GenericDialect{}, RowAfter(cols[2:], "a.md"), 1, `"document"."path" > $2`, []any{"a.md"}},
		{"an EXISTS, numbered", dao.GenericDialect{}, in, 1,
			`EXISTS (SELECT 1 FROM "doc_facet" WHERE "doc_facet"."doc_id" = "document"."id"` +
				` AND ("doc_facet"."workspace_id" = "document"."workspace_id") AND ("doc_facet"."field" = $2)` +
				` AND ("doc_facet"."value" IN ($3, $4)))`,
			[]any{"status", "a", "b"}},
		{"an EXISTS with no condition", positional{}, MatchIn("doc_diagnostic", "doc_id", "document", "id"), 0,
			`EXISTS (SELECT 1 FROM "doc_diagnostic" WHERE "doc_diagnostic"."doc_id" = "document"."id"` +
				` AND ("doc_diagnostic"."workspace_id" = "document"."workspace_id"))`, nil},
	} {
		next := c.from
		sql, args := c.p.ToSQL(c.d, &next)
		if sql != c.sql || !reflect.DeepEqual(args, c.args) || next != c.from+len(c.args) {
			t.Errorf("%s:\n%s %v (next %d)\nwant\n%s %v (next %d)", c.name, sql, args, next, c.sql, c.args, c.from+len(c.args))
		}
	}
}
