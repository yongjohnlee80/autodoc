package store

import (
	"reflect"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/sqlite"
)

// Correlated renders inside its outer statement: its conds take the statement's next placeholders,
// in order, after a bind before it and before one after it, nested groups keep their parentheses, and
// NOT EXISTS is the negation. Both placeholder styles: numbered ($n, PostgreSQL's) and positional (?).
func TestCorrelatedRendersWithTheOuterPlaceholders(t *testing.T) {
	inner := Correlated(false, "t", "c", "a", "k",
		dao.Eq(`"t"."y"`, 2), dao.Or(dao.Eq(`"t"."z"`, 3), dao.IsNull(`"t"."w"`)))
	p := dao.And(dao.Eq(`"a"."x"`, 1), inner, dao.Eq(`"a"."v"`, 4))
	for _, c := range []struct {
		name string
		d    dao.Dialect
		want string
	}{
		{"numbered", dao.GenericDialect{}, `("a"."x" = $1 AND EXISTS (SELECT 1 FROM "t" WHERE "t"."c" = "a"."k" AND ("t"."y" = $2) AND (("t"."z" = $3 OR "t"."w" IS NULL))) AND "a"."v" = $4)`},
		{"positional", sqlite.SqliteDialect{}, `("a"."x" = ? AND EXISTS (SELECT 1 FROM "t" WHERE "t"."c" = "a"."k" AND ("t"."y" = ?) AND (("t"."z" = ? OR "t"."w" IS NULL))) AND "a"."v" = ?)`},
	} {
		next := 0
		got, args := p.ToSQL(c.d, &next)
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
		if !reflect.DeepEqual(args, []any{1, 2, 3, 4}) || next != 4 {
			t.Errorf("%s: args %v, next %d; want 1..4 in order, next 4", c.name, args, next)
		}
	}
	next := 0
	got, args := Correlated(true, "t", "c", "a", "k").ToSQL(dao.GenericDialect{}, &next)
	if got != `NOT EXISTS (SELECT 1 FROM "t" WHERE "t"."c" = "a"."k")` || len(args) != 0 || next != 0 {
		t.Errorf("NOT EXISTS with no conds: %s %v %d", got, args, next)
	}
}
