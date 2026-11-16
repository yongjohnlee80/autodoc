package store

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/errs"
)

// COPYING A SQLITE STORE INTO POSTGRESQL (docs/decisions/store.md#copying-a-sqlite-store-into-postgresql)
// — CopySQLite fills an empty store on PostgreSQL from a SQLite store, all or nothing:
//
//   - both stores are held for the whole copy (the destination's lease, the source's writer), and
//     must name the same applied scripts;
//   - the source is read from one snapshot, the destination written in one fenced transaction,
//     table by table in foreign-key order, streamed through COPY in bounded batches;
//   - rows keep their ids, and each identity sequence is set to SQLite's high-water;
//   - every value that does not fit its column is listed, from one pass, and nothing is committed;
//   - before the commit: each table's count equals the source's, every sealed secret opens with the
//     destination's key, and the lease is fenced again; after it, the tables are analyzed.
//
// The copy is a new store: the destination keeps its own store_id. Its key must be the source's:
// written when it has none, refused when it has another.

// The copy's batch bounds: a batch is sent once it holds copyBatchRows rows or copyBatchBytes of
// values, so a table is never held in memory whole.
const (
	copyBatchRows  = 10_000
	copyBatchBytes = 8 << 20
)

// copyBadShown is how many refused values a BadValuesError lists; it counts the rest.
const copyBadShown = 50

// copyLeftOut are the tables a copy never carries: the scripts' ledger (each store keeps its own),
// PostgreSQL's writer lease (runtime state) and its store_identity (the copy is a new store).
var copyLeftOut = []string{deploy.DefaultLedger, LeaseTable, "store_identity"}

var (
	// ErrCopyScripts is a copy refused because the two stores name different applied scripts.
	ErrCopyScripts = errs.Sentinel(errs.ErrPrecondition, "store: the source's and the destination's scripts differ")
	// ErrCopyTables is a copy refused because a table or a column is in one store only.
	ErrCopyTables = errs.Sentinel(errs.ErrPrecondition, "store: the source's and the destination's tables differ")
	// ErrCopyNotEmpty is a copy refused because the destination already holds rows.
	ErrCopyNotEmpty = errs.Sentinel(errs.ErrPrecondition, "store: the destination is not empty")
	// ErrCopyKey is a copy refused because the destination's key is not the source's.
	ErrCopyKey = errs.Sentinel(errs.ErrPrecondition, "store: the destination's key is not the source's")
	// ErrCopyValues is a copy refused for values that do not fit their columns (BadValuesError).
	ErrCopyValues = errs.Sentinel(errs.ErrInvalidArgument, "store: values that do not fit their columns")
	// ErrCopyCheck is a copy rolled back by its own check before the commit.
	ErrCopyCheck = errs.Sentinel(errs.ErrPrecondition, "store: the copy failed its check")
)

// CopyReport is what a copy did.
type CopyReport struct {
	Tables []CopiedTable // in the order they were loaded
	// Key says what became of the source's key: "copied" (the destination had none), "matched" (it
	// had the same) or "none" (the source has no key file)
	Key string
}

// CopiedTable is one table a copy loaded.
type CopiedTable struct {
	Name string
	Rows int64
	// Identity is the table's identity column, and Next the id its next row takes; "" and 0 for a
	// table with none
	Identity string
	Next     int64
}

// BadValue is one value a copy refused: where it is and why.
type BadValue struct {
	Table, Column string
	Row           string // the row's primary key, as col=value
	Problem       string
}

func (b BadValue) String() string {
	return fmt.Sprintf("%s.%s (%s): %s", b.Table, b.Column, b.Row, b.Problem)
}

// BadValuesError is every value a copy refused, the first copyBadShown listed and the rest counted.
type BadValuesError struct {
	Values []BadValue
	More   int
}

func (e *BadValuesError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "store: %d values do not fit their columns; nothing was copied:", len(e.Values)+e.More)
	for _, v := range e.Values {
		b.WriteString("\n  " + v.String())
	}
	if e.More > 0 {
		fmt.Fprintf(&b, "\n  and %d more", e.More)
	}
	return b.String()
}

func (e *BadValuesError) Unwrap() error { return ErrCopyValues }

// copyColumn is one column a copy writes: its name, its PostgreSQL type (bigint, text or bytea)
// and whether it takes NULL.
type copyColumn struct {
	name, kind string
	nullable   bool
}

// copyTable is one table a copy loads.
type copyTable struct {
	name     string
	cols     []copyColumn // the destination's, generated ones left out, in their order
	key      []string     // the primary key's columns
	identity string       // the identity column, "" when none
}

// copyBatchHook, set by a cell, runs after each batch is sent, with its table, rows and bytes.
var copyBatchHook func(table string, rows, bytes int)

// copyBeforeCommit, set by a cell, runs in the copy's transaction just before its last check and
// commit: a cell holds the transaction there, busy, as a long copy would.
var copyBeforeCommit func(q dao.Querier)

// CopySQLite copies src, a SQLite store, into dst, an empty store on PostgreSQL, in one
// transaction. Both must be open: dst with its lease, src with its writer.
func CopySQLite(ctx context.Context, dst, src *Store) (CopyReport, error) {
	var rep CopyReport
	switch {
	case dst.pg == nil:
		return rep, errors.New("store: a copy's destination is a store on PostgreSQL")
	case src.pg != nil:
		return rep, fmt.Errorf("%w: a copy's source is a SQLite store", ErrNotSQLite)
	}
	if diff := scriptsDiffer(src.status, dst.status); diff != "" {
		return rep, fmt.Errorf("%w: %s", ErrCopyScripts, diff)
	}
	tables, err := copyPlan(ctx, dst, src)
	if err != nil {
		return rep, err
	}
	for _, t := range tables {
		var has bool
		if err := scanOne(ctx, dst.w, `SELECT EXISTS (SELECT 1 FROM `+quoteIdent(t.name)+`)`, []any{&has}); err != nil {
			return rep, fmt.Errorf("store: reading the destination's %s: %w", t.name, err)
		}
		if has {
			return rep, fmt.Errorf("%w: its %s has rows (copy into a new schema, or drop this one first)", ErrCopyNotEmpty, t.name)
		}
	}
	if rep.Key, err = shareKey(src.keys.path, dst.keys.path); err != nil {
		return rep, err
	}
	committed := false
	if rep.Key == "copied" {
		defer func() {
			if !committed {
				_ = os.Remove(dst.keys.path) // the destination is left as it was: empty, and keyless
			}
		}()
	}
	if rep.Tables, err = copyRows(ctx, dst, src, tables); err != nil {
		return rep, err
	}
	committed = true
	// a bulk load leaves no statistics: the first searches would plan without them until
	// autovacuum's analyze came round
	for _, t := range tables {
		if _, err := dst.w.ExecContext(ctx, `ANALYZE `+quoteIdent(t.name)); err != nil {
			return rep, fmt.Errorf("store: the copy is committed, but analyzing %s failed (run ANALYZE): %w", t.name, err)
		}
	}
	return rep, nil
}

// scriptsDiffer names the first script one of two stores has applied and the other has not; ""
// when they name the same.
func scriptsDiffer(src, dst deploy.Status) string {
	a := slices.Sorted(slices.Values(append(slices.Clone(src.Applied), src.Pending...)))
	b := slices.Sorted(slices.Values(append(slices.Clone(dst.Applied), dst.Pending...)))
	for i := 0; i < len(a) || i < len(b); i++ {
		switch {
		case i >= len(b) || (i < len(a) && a[i] < b[i]):
			return fmt.Sprintf("the source has %s, the destination has not", a[i])
		case i >= len(a) || a[i] > b[i]:
			return fmt.Sprintf("the destination has %s, the source has not", b[i])
		}
	}
	return ""
}

// shareKey makes the destination's key the source's: "copied" when the destination had none,
// "matched" when it had the same, "none" when the source has no key file. Another key is
// ErrCopyKey. A key's bytes never reach an error.
func shareKey(srcPath, dstPath string) (string, error) {
	sk, err := readKey(srcPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "none", nil // nothing was ever sealed in the source
	}
	if err != nil {
		return "", fmt.Errorf("store: the source's key: %w", err)
	}
	dk, err := readKey(dstPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := writeKey(dstPath, sk); err != nil {
			return "", fmt.Errorf("store: writing the source's key to %s: %w", dstPath, err)
		}
		return "copied", nil
	case err != nil:
		return "", fmt.Errorf("store: the destination's key: %w", err)
	case subtle.ConstantTimeCompare(sk, dk) != 1:
		return "", fmt.Errorf("%w: %s does not hold %s's key; give the destination a key_file of its own, or none", ErrCopyKey, dstPath, srcPath)
	}
	return "matched", nil
}

// copyPlan is the tables to copy, parents before the tables that reference them, with their
// columns read from the destination's catalog and checked against the source's.
func copyPlan(ctx context.Context, dst, src *Store) ([]copyTable, error) {
	schema := dst.pg.loc.Schema
	byName := map[string]*copyTable{}
	err := eachRow(ctx, dst.w, `SELECT table_name, column_name, data_type, is_nullable = 'YES', is_identity = 'YES', is_generated <> 'NEVER'
		FROM information_schema.columns WHERE table_schema = $1 ORDER BY table_name, ordinal_position`, func(scan func(...any) error) error {
		var table, col, kind string
		var nullable, identity, generated bool
		if err := scan(&table, &col, &kind, &nullable, &identity, &generated); err != nil {
			return err
		}
		if slices.Contains(copyLeftOut, table) || generated {
			return nil
		}
		t := byName[table]
		if t == nil {
			t = &copyTable{name: table}
			byName[table] = t
		}
		switch kind {
		case "bigint", "text", "bytea":
		default:
			return fmt.Errorf("store: the destination's %s.%s is %s, which a copy has no rule for", table, col, kind)
		}
		t.cols = append(t.cols, copyColumn{name: col, kind: kind, nullable: nullable})
		if identity {
			t.identity = col
		}
		return nil
	}, schema)
	if err != nil {
		return nil, fmt.Errorf("store: reading the destination's columns: %w", err)
	}
	err = eachRow(ctx, dst.w, `SELECT c.relname, a.attname FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY (i.indkey)
		WHERE i.indisprimary AND n.nspname = $1 ORDER BY c.relname, array_position(i.indkey::int2[], a.attnum)`, func(scan func(...any) error) error {
		var table, col string
		if err := scan(&table, &col); err != nil {
			return err
		}
		if t := byName[table]; t != nil {
			t.key = append(t.key, col)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, fmt.Errorf("store: reading the destination's primary keys: %w", err)
	}
	parents := map[string][]string{}
	err = eachRow(ctx, dst.w, `SELECT c.relname, f.relname FROM pg_constraint k
		JOIN pg_class c ON c.oid = k.conrelid JOIN pg_class f ON f.oid = k.confrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace WHERE k.contype = 'f' AND n.nspname = $1`, func(scan func(...any) error) error {
		var child, parent string
		if err := scan(&child, &parent); err != nil {
			return err
		}
		if child != parent {
			parents[child] = append(parents[child], parent)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, fmt.Errorf("store: reading the destination's foreign keys: %w", err)
	}
	if err := sameTables(ctx, src, byName); err != nil {
		return nil, err
	}
	return inParentOrder(byName, parents)
}

// sameTables refuses a source whose tables or columns are not the destination's.
func sameTables(ctx context.Context, src *Store, dst map[string]*copyTable) error {
	var names []string
	err := eachRow(ctx, src.w, `SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT LIKE 'chunk\_fts%' ESCAPE '\'`, func(scan func(...any) error) error {
		var n string
		if err := scan(&n); err != nil {
			return err
		}
		if !slices.Contains(copyLeftOut, n) {
			names = append(names, n)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("store: reading the source's tables: %w", err)
	}
	sort.Strings(names)
	for _, n := range names {
		t := dst[n]
		if t == nil {
			return fmt.Errorf("%w: the source has a table %s, the destination has not", ErrCopyTables, n)
		}
		var cols []string
		err := eachRow(ctx, src.w, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, func(scan func(...any) error) error {
			var c string
			if err := scan(&c); err != nil {
				return err
			}
			cols = append(cols, c)
			return nil
		}, n)
		if err != nil {
			return fmt.Errorf("store: reading the source's %s: %w", n, err)
		}
		want := make([]string, len(t.cols))
		for i, c := range t.cols {
			want[i] = c.name
		}
		sort.Strings(cols)
		sort.Strings(want)
		if !slices.Equal(cols, want) {
			return fmt.Errorf("%w: %s's columns are %s in the source, %s in the destination", ErrCopyTables, n, strings.Join(cols, ", "), strings.Join(want, ", "))
		}
	}
	for n := range dst {
		if !slices.Contains(names, n) {
			return fmt.Errorf("%w: the destination has a table %s, the source has not", ErrCopyTables, n)
		}
	}
	return nil
}

// inParentOrder is the tables with every table after the ones it references, by name otherwise.
func inParentOrder(byName map[string]*copyTable, parents map[string][]string) ([]copyTable, error) {
	var out []copyTable
	done := map[string]bool{}
	names := slices.Sorted(maps.Keys(byName))
	for len(out) < len(names) {
		progressed := false
		for _, n := range names {
			if done[n] {
				continue
			}
			ready := true
			for _, p := range parents[n] {
				if _, copied := byName[p]; copied && !done[p] {
					ready = false
					break
				}
			}
			if ready {
				done[n] = true
				out = append(out, *byName[n])
				progressed = true
			}
		}
		if !progressed {
			return nil, errors.New("store: the destination's foreign keys form a cycle; a copy cannot order its tables")
		}
	}
	return out, nil
}

// copyRows loads every table in one transaction of dst's fenced writer, from one read transaction
// of src, and commits only when every check holds.
func copyRows(ctx context.Context, dst, src *Store, tables []copyTable) ([]CopiedTable, error) {
	copier, ok := dst.w.Dialect().(dao.Copier)
	if !ok {
		return nil, fmt.Errorf("%w: the destination's engine has no COPY", errs.ErrUnsupported)
	}
	stx, err := src.r.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: reading the source: %w", err)
	}
	defer func() { _ = stx.Rollback() }()
	dtx, err := dst.w.Begin(ctx) // its fence: the lease is this process's, and held until the end
	if err != nil {
		return nil, fmt.Errorf("store: writing the destination: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = dtx.Rollback()
		}
	}()
	bad := &BadValuesError{}
	var out []CopiedTable
	for _, t := range tables {
		n, err := copyTableRows(ctx, copier, dtx, stx, t, bad)
		if err != nil {
			return nil, err
		}
		out = append(out, CopiedTable{Name: t.name, Rows: n, Identity: t.identity})
	}
	if len(bad.Values) > 0 {
		return nil, bad
	}
	for i, t := range tables {
		if err := checkCount(ctx, dtx, stx, t.name); err != nil {
			return nil, err
		}
		if t.identity != "" {
			if out[i].Next, err = setHighWater(ctx, dtx, stx, dst.pg.loc.Schema, t); err != nil {
				return nil, err
			}
		}
	}
	all, err := dst.sealedIn(ctx, dtx)
	if err != nil {
		return nil, err
	}
	shut, err := openEach(dst.keys.path, all)
	if err != nil {
		return nil, err
	}
	if len(shut) > 0 {
		return nil, fmt.Errorf("%w: with the destination's key: %s", ErrCopyCheck, strings.Join(shut, "; "))
	}
	if copyBeforeCommit != nil {
		copyBeforeCommit(dtx)
	}
	// the transaction may have run longer than the lease: it commits only while the lease is still
	// this process's (renewed throughout, or the copy is rolled back)
	if err := dst.pg.fence(ctx, dtx); err != nil {
		return nil, fmt.Errorf("store: the copy outlasted the destination's lease and was rolled back: %w", err)
	}
	if err := dtx.Commit(); err != nil {
		return nil, fmt.Errorf("store: committing the copy: %w", err)
	}
	committed = true
	return out, nil
}

// copyTableRows streams one table's rows from stx into dtx in bounded batches. A value that does
// not fit is added to bad, and from then on rows are only read, to find the rest.
func copyTableRows(ctx context.Context, copier dao.Copier, dtx dao.TxConn, stx dao.TxConn, t copyTable, bad *BadValuesError) (int64, error) {
	names := make([]string, len(t.cols))
	for i, c := range t.cols {
		names[i] = quoteIdent(c.name)
	}
	rows, err := stx.QueryContext(ctx, `SELECT `+strings.Join(names, ", ")+` FROM `+quoteIdent(t.name))
	if err != nil {
		return 0, fmt.Errorf("store: reading the source's %s: %w", t.name, err)
	}
	defer func() { _ = rows.Close() }()
	cols := make([]string, len(t.cols))
	for i, c := range t.cols {
		cols[i] = c.name
	}
	var (
		batch [][]any
		size  int
		n     int64
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if len(bad.Values) == 0 && bad.More == 0 {
			if _, err := copier.Copy(ctx, dtx, t.name, cols, batch); err != nil {
				return fmt.Errorf("store: loading %s: %w", t.name, err)
			}
		}
		if copyBatchHook != nil {
			copyBatchHook(t.name, len(batch), size)
		}
		batch, size = nil, 0
		return nil
	}
	raw := make([]any, len(t.cols))
	ptrs := make([]any, len(t.cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return 0, fmt.Errorf("store: reading the source's %s: %w", t.name, err)
		}
		row := make([]any, len(t.cols))
		fits := true
		for i, c := range t.cols {
			v, w, problem := fitValue(raw[i], c)
			if problem != "" {
				fits = false
				if len(bad.Values) < copyBadShown {
					bad.Values = append(bad.Values, BadValue{Table: t.name, Column: c.name, Row: rowKey(t, raw), Problem: problem})
				} else {
					bad.More++
				}
				continue
			}
			row[i] = v
			size += w
		}
		n++
		if !fits || len(bad.Values) > 0 {
			continue // nothing more is sent: the copy will be refused, and the rest is only read
		}
		batch = append(batch, row)
		if len(batch) >= copyBatchRows || size >= copyBatchBytes {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: reading the source's %s: %w", t.name, err)
	}
	return n, flush()
}

// fitValue is v as c takes it, with its size in a batch; or the problem that refuses it.
func fitValue(v any, c copyColumn) (any, int, string) {
	if v == nil {
		if !c.nullable {
			return nil, 0, "NULL in a column that takes none"
		}
		return nil, 0, ""
	}
	switch c.kind {
	case "bigint":
		switch x := v.(type) {
		case int64:
			return x, 8, ""
		case float64:
			if x == math.Trunc(x) && x >= math.MinInt64 && x < math.MaxInt64 {
				return int64(x), 8, ""
			}
			return nil, 0, fmt.Sprintf("the number %v in an integer column", x)
		case string:
			return nil, 0, fmt.Sprintf("text %s in an integer column", shortQuote(x))
		default:
			return nil, 0, fmt.Sprintf("a %T in an integer column", v)
		}
	case "text":
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case []byte:
			s = string(x)
		default:
			return nil, 0, fmt.Sprintf("a %T in a text column", v)
		}
		switch {
		case strings.IndexByte(s, 0) >= 0:
			return nil, 0, "text with a NUL byte, which PostgreSQL's text cannot hold"
		case !utf8.ValidString(s):
			return nil, 0, "text that is not valid UTF-8"
		}
		return s, len(s), ""
	case "bytea":
		switch x := v.(type) {
		case []byte:
			return x, len(x), ""
		case string:
			return []byte(x), len(x), ""
		default:
			return nil, 0, fmt.Sprintf("a %T in a blob column", v)
		}
	}
	return nil, 0, fmt.Sprintf("a column of type %s", c.kind)
}

// rowKey names a row by its primary key, as col=value.
func rowKey(t copyTable, raw []any) string {
	var parts []string
	for _, k := range t.key {
		for i, c := range t.cols {
			if c.name == k {
				parts = append(parts, k+"="+shortValue(raw[i]))
			}
		}
	}
	if len(parts) == 0 {
		return "no primary key"
	}
	return strings.Join(parts, ", ")
}

func shortValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(x, 10)
	case string:
		return shortQuote(x)
	case []byte:
		return shortQuote(string(x))
	}
	return fmt.Sprint(v)
}

// shortQuote quotes s, cut to 40 bytes.
func shortQuote(s string) string {
	if len(s) > 40 {
		return strconv.Quote(s[:40]) + "…"
	}
	return strconv.Quote(s)
}

// checkCount refuses a table whose rows in the destination are not the source's count.
func checkCount(ctx context.Context, dtx, stx dao.TxConn, table string) error {
	var want, got int64
	q := `SELECT count(*) FROM ` + quoteIdent(table)
	if err := scanOne(ctx, stx, q, []any{&want}); err != nil {
		return fmt.Errorf("store: counting the source's %s: %w", table, err)
	}
	if err := scanOne(ctx, dtx, q, []any{&got}); err != nil {
		return fmt.Errorf("store: counting the destination's %s: %w", table, err)
	}
	if got != want {
		return fmt.Errorf("%w: %s has %d rows in the destination, %d in the source", ErrCopyCheck, table, got, want)
	}
	return nil
}

// setHighWater sets t's identity sequence to SQLite's high-water: the larger of the column's MAX and
// sqlite_sequence's seq (an AUTOINCREMENT table's, which deletes above MAX leave higher). It returns
// the id the next row takes.
func setHighWater(ctx context.Context, dtx, stx dao.TxConn, schema string, t copyTable) (int64, error) {
	var high int64
	if err := scanOne(ctx, dtx, `SELECT coalesce(max(`+quoteIdent(t.identity)+`), 0) FROM `+quoteIdent(t.name), []any{&high}); err != nil {
		return 0, fmt.Errorf("store: %s's highest id: %w", t.name, err)
	}
	var seqs int64
	if err := scanOne(ctx, stx, `SELECT count(*) FROM sqlite_master WHERE name = 'sqlite_sequence'`, []any{&seqs}); err != nil {
		return 0, err
	}
	if seqs > 0 {
		var seq int64
		err := scanOne(ctx, stx, `SELECT seq FROM sqlite_sequence WHERE name = ?`, []any{&seq}, t.name)
		switch {
		case err == nil:
			high = max(high, seq)
		case !errors.Is(err, dao.ErrNoRows):
			return 0, fmt.Errorf("store: %s's sequence in the source: %w", t.name, err)
		}
	}
	if high == 0 {
		return 1, nil // an empty table: its sequence starts where it was made to
	}
	var set int64
	err := scanOne(ctx, dtx, `SELECT setval(pg_get_serial_sequence($1, $2), $3, true)`, []any{&set},
		quoteIdent(schema)+"."+quoteIdent(t.name), t.identity, high)
	if err != nil {
		return 0, fmt.Errorf("store: setting %s's sequence: %w", t.name, err)
	}
	return set + 1, nil
}

// scanOne scans query's one row on q into dest: dao.ErrNoRows when it has none.
func scanOne(ctx context.Context, q dao.Querier, query string, dest []any, args ...any) error {
	found := false
	err := eachRow(ctx, q, query, func(scan func(...any) error) error {
		if found {
			return nil
		}
		found = true
		return scan(dest...)
	}, args...)
	if err == nil && !found {
		err = dao.ErrNoRows
	}
	return err
}

// quoteIdent quotes a catalog's name as an SQL identifier.
func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
