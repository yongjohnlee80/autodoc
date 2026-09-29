package index

import (
	"context"
	"errors"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// Raw reads of the store file, for the tests that check rows the API does not show. The index
// itself reads and writes only through its scope.

var errNoRow = errors.New("test: no row")

func scanOne(ctx context.Context, q dao.Querier, dst any, query string, args ...any) error {
	return scanRow(ctx, q, []any{dst}, query, args...)
}

func scanRow(ctx context.Context, q dao.Querier, dst []any, query string, args ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return errNoRow
	}
	if err := rows.Scan(dst...); err != nil {
		return err
	}
	return rows.Err()
}

// exec runs q on the store file directly.
func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.raw.ExecContext(context.Background(), q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

// codes is loadCodes over every document, in a read transaction.
func (e *env) codes(fp string) (map[int64][]code, error) {
	var out map[int64][]code
	err := e.store.read(context.Background(), func(tx *store.Tx) error {
		var err error
		out, err = e.store.loadCodes(tx, fp, nil)
		return err
	})
	return out, err
}
