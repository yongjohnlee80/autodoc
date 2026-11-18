//go:build server

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
)

// copyAutoincrement are the tables whose ids SQLite never reuses (AUTOINCREMENT): a copy keeps
// their high-water, which deletes above MAX leave higher.
var copyAutoincrement = []string{"event", "todo_change", "inbox_change"}

// copyStores is a fresh SQLite store and an empty store on PostgreSQL, both closed with the cell.
func copyStores(t *testing.T) (dst, src *Store) {
	t.Helper()
	ctx := context.Background()
	src, err := Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	dst, err = OpenAt(ctx, pgLocation(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	return dst, src
}

// copyChecked are the columns a CHECK holds to a few values, and the value a fixture row takes.
var copyChecked = map[string]any{
	"workspace.embedding_policy": "always", "workspace.destination": "sqlite", "workspace.vector_index": "hnsw",
	"workspace.include_empty": int64(1), "workspace.graph_only_set": int64(1), "workspace.todo_graph_only": int64(1),
	"workspace_pattern.kind": "include", "embedding_provider.kind": "ollama", "ranker.kind": "tei",
	"workspace_connection.role": "source", "workspace_connection.engine": "sqlite", "todo_change.state": "pending",
}

// fixtureValue is row i's value of t's column c: the same for every table's column of a kind, so a
// foreign key (an id, a model's fp) finds its parent's row i. Row 2 leaves every nullable column
// NULL; row 3's text is not plain ASCII. The secrets are sealed with src's key, as the store seals
// them.
func fixtureValue(t *testing.T, src *Store, tb copyTable, c copyColumn, i int64) any {
	t.Helper()
	if c.nullable && i == 2 {
		return nil
	}
	sealed := func(aad string) any {
		b, err := src.keys.seal([]byte(fmt.Sprintf("secret-%d", i)), aad)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	switch tb.name + "." + c.name {
	case "embedding_provider.api_key":
		return sealed(src.providers().aad(i))
	case "ranker.api_key":
		return sealed(src.rankers().aad(i))
	case "workspace_connection.dsn":
		return sealed(dsnAAD(i, RoleSource))
	}
	if v, ok := copyChecked[tb.name+"."+c.name]; ok {
		return v
	}
	switch c.kind {
	case "bigint":
		return i
	case "bytea":
		return []byte{byte(i), 0, 0xff, byte(i)}
	}
	if i == 3 {
		return "v3 \"é\" ✓\n'"
	}
	return fmt.Sprintf("v%d", i)
}

// insertFixtureRow writes row i of tb into the SQLite store.
func insertFixtureRow(t *testing.T, src *Store, tb copyTable, i int64) {
	t.Helper()
	names, marks, args := make([]string, len(tb.cols)), make([]string, len(tb.cols)), make([]any, len(tb.cols))
	for k, c := range tb.cols {
		names[k], marks[k], args[k] = quoteIdent(c.name), "?", fixtureValue(t, src, tb, c, i)
	}
	q := `INSERT INTO ` + quoteIdent(tb.name) + ` (` + strings.Join(names, ", ") + `) VALUES (` + strings.Join(marks, ", ") + `)`
	if _, err := src.w.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("the fixture's %s row %d: %v", tb.name, i, err)
	}
}

// fillSource writes three rows into every table a copy carries, parents first, and leaves each
// AUTOINCREMENT table's high-water at 5 (rows 4 and 5 written, then deleted). It returns the plan.
func fillSource(t *testing.T, dst, src *Store) []copyTable {
	t.Helper()
	ctx := context.Background()
	plan, err := copyPlan(ctx, dst, src)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range plan {
		for i := int64(1); i <= 3; i++ {
			insertFixtureRow(t, src, tb, i)
		}
		if slices.Contains(copyAutoincrement, tb.name) {
			insertFixtureRow(t, src, tb, 4)
			insertFixtureRow(t, src, tb, 5)
			if _, err := src.w.ExecContext(ctx, `DELETE FROM `+quoteIdent(tb.name)+` WHERE `+quoteIdent(tb.identity)+` > 3`); err != nil {
				t.Fatal(err)
			}
		}
	}
	return plan
}

// tableRows is every row of tb through q, ordered by its key, each value as Go reads it back from
// either engine: int64, string or []byte.
func tableRows(t *testing.T, q dao.Querier, tb copyTable) [][]any {
	t.Helper()
	names := make([]string, len(tb.cols))
	for i, c := range tb.cols {
		names[i] = quoteIdent(c.name)
	}
	order := names
	if len(tb.key) > 0 {
		order = nil
		for _, k := range tb.key {
			order = append(order, quoteIdent(k))
		}
	}
	var out [][]any
	err := eachRow(context.Background(), q, `SELECT `+strings.Join(names, ", ")+` FROM `+quoteIdent(tb.name)+` ORDER BY `+strings.Join(order, ", "), func(scan func(...any) error) error {
		raw := make([]any, len(tb.cols))
		ptrs := make([]any, len(raw))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := scan(ptrs...); err != nil {
			return err
		}
		for i, c := range tb.cols {
			if b, ok := raw[i].([]byte); ok && c.kind == "text" {
				raw[i] = string(b)
			}
		}
		out = append(out, raw)
		return nil
	})
	if err != nil {
		t.Fatalf("reading %s: %v", tb.name, err)
	}
	return out
}

// A SQLite store with every table filled copies into an empty store on PostgreSQL: each table
// compares row for row, each identity sequence's next id is SQLite's (past an AUTOINCREMENT table's
// deleted rows), the sealed secrets open with the key the copy wrote, the destination keeps its own
// store_id, and every copied table is analyzed. The mutants, named here: the sequence set from MAX
// alone (the AUTOINCREMENT tables would reuse 4 and 5), and no ANALYZE.
func TestASQLiteStoreCopiesIntoPostgres(t *testing.T) {
	ctx := context.Background()
	dst, src := copyStores(t)
	plan := fillSource(t, dst, src)
	if len(plan) < 40 {
		t.Fatalf("the plan has %d tables; the store has more than 40", len(plan))
	}
	id, err := dst.ID()
	if err != nil {
		t.Fatal(err)
	}
	rep, err := CopySQLite(ctx, dst, src)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Key != "copied" {
		t.Errorf("the key: %q, want copied", rep.Key)
	}
	sk, _ := os.ReadFile(src.keys.path)
	if dk, err := os.ReadFile(dst.keys.path); err != nil || !bytes.Equal(sk, dk) {
		t.Errorf("the destination's key file is not the source's: %v", err)
	}
	if len(rep.Tables) != len(plan) {
		t.Errorf("the report names %d tables, the plan %d", len(rep.Tables), len(plan))
	}
	for i, tb := range plan {
		want, got := tableRows(t, src.w, tb), tableRows(t, dst.r, tb)
		if len(want) != 3 || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the destination's rows\n%v\nthe source's\n%v", tb.name, got, want)
		}
		if rep.Tables[i].Name != tb.name || rep.Tables[i].Rows != 3 {
			t.Errorf("the report's %d: %+v, want %s with 3 rows", i, rep.Tables[i], tb.name)
		}
		if tb.identity == "" {
			continue
		}
		next := int64(4)
		if slices.Contains(copyAutoincrement, tb.name) {
			next = 6
		}
		var got64 int64
		if err := scanOne(ctx, dst.w, `SELECT nextval(pg_get_serial_sequence($1, $2))`, []any{&got64},
			quoteIdent(dst.pg.loc.Schema)+"."+quoteIdent(tb.name), tb.identity); err != nil {
			t.Fatal(err)
		}
		if got64 != next || rep.Tables[i].Next != next {
			t.Errorf("%s's next %s: %d, reported %d, want %d", tb.name, tb.identity, got64, rep.Tables[i].Next, next)
		}
	}
	if _, key, err := dst.ProviderWithKey(ctx, "v1"); err != nil || key != "secret-1" {
		t.Errorf("the copied provider's key: %q, %v", key, err)
	}
	if again, err := dst.ID(); err != nil || again != id {
		t.Errorf("the destination's store_id: %q, %v; want its own %q", again, err, id)
	}
	// the statistics reach pg_stat_user_tables within a second or so of the ANALYZE
	deadline := time.Now().Add(15 * time.Second)
	for {
		var missing []string
		err := eachRow(ctx, dst.w, `SELECT relname FROM pg_stat_user_tables WHERE schemaname = $1 AND last_analyze IS NULL`, func(scan func(...any) error) error {
			var n string
			if err := scan(&n); err != nil {
				return err
			}
			if !slices.Contains(copyLeftOut, n) {
				missing = append(missing, n)
			}
			return nil
		}, dst.pg.loc.Schema)
		if err != nil {
			t.Fatal(err)
		}
		if len(missing) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tables never analyzed: %s", strings.Join(missing, ", "))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// emptyAfter fails the cell when any of plan's tables holds a row in dst: a refused copy leaves the
// destination as it found it.
func emptyAfter(t *testing.T, dst *Store, plan []copyTable) {
	t.Helper()
	for _, tb := range plan {
		var n int64
		if err := scanOne(context.Background(), dst.r, `SELECT count(*) FROM `+quoteIdent(tb.name), []any{&n}); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("the refused copy left %d rows in %s", n, tb.name)
		}
	}
}

// A copy into a store that already holds rows is refused, naming the table, and a second copy of
// the same store is that.
func TestACopyIntoAStoreWithRowsIsRefused(t *testing.T) {
	ctx := context.Background()
	dst, src := copyStores(t)
	fillSource(t, dst, src)
	if _, err := CopySQLite(ctx, dst, src); err != nil {
		t.Fatal(err)
	}
	_, err := CopySQLite(ctx, dst, src)
	if !errors.Is(err, ErrCopyNotEmpty) || !strings.Contains(err.Error(), "has rows") {
		t.Errorf("a second copy: %v, want ErrCopyNotEmpty naming the table", err)
	}
}

// Two stores whose applied scripts differ are refused before anything is read, naming the script.
func TestACopyBetweenStoresOfOtherScriptsIsRefused(t *testing.T) {
	dst, src := copyStores(t)
	plan := fillSource(t, dst, src)
	src.status.Applied = append(slices.Clone(src.status.Applied), "999999_update_later.sql")
	_, err := CopySQLite(context.Background(), dst, src)
	if !errors.Is(err, ErrCopyScripts) || !strings.Contains(err.Error(), "999999_update_later.sql") {
		t.Errorf("other scripts: %v, want ErrCopyScripts naming the script", err)
	}
	emptyAfter(t, dst, plan)
}

// The destination's key: one holding the source's is accepted; another is refused before any row
// moves, the key file left as it was; and a copy refused after the key was written takes it back.
// The mutant, named here: the key not checked (the copy commits secrets no key opens).
func TestACopyKeepsTheSourcesKey(t *testing.T) {
	ctx := context.Background()
	t.Run("matching", func(t *testing.T) {
		dst, src := copyStores(t)
		fillSource(t, dst, src)
		sk, err := os.ReadFile(src.keys.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeKey(dst.keys.path, sk); err != nil {
			t.Fatal(err)
		}
		rep, err := CopySQLite(ctx, dst, src)
		if err != nil || rep.Key != "matched" {
			t.Errorf("a destination with the source's key: %q, %v", rep.Key, err)
		}
	})
	t.Run("another", func(t *testing.T) {
		dst, src := copyStores(t)
		plan := fillSource(t, dst, src)
		other := bytes.Repeat([]byte{7}, keySize)
		if err := writeKey(dst.keys.path, other); err != nil {
			t.Fatal(err)
		}
		_, err := CopySQLite(ctx, dst, src)
		if !errors.Is(err, ErrCopyKey) {
			t.Errorf("a destination with another key: %v, want ErrCopyKey", err)
		}
		if got, _ := os.ReadFile(dst.keys.path); !bytes.Equal(got, other) {
			t.Error("the refused copy changed the destination's key file")
		}
		emptyAfter(t, dst, plan)
	})
	t.Run("taken back", func(t *testing.T) {
		dst, src := copyStores(t)
		plan := fillSource(t, dst, src)
		if _, err := src.w.ExecContext(ctx, `UPDATE preference SET value = 'x' || char(0) || 'y' WHERE name = 'v1'`); err != nil {
			t.Fatal(err)
		}
		if _, err := CopySQLite(ctx, dst, src); !errors.Is(err, ErrCopyValues) {
			t.Fatalf("a copy with a bad value: %v", err)
		}
		if _, err := os.Stat(dst.keys.path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the refused copy left the key it wrote: %v", err)
		}
		emptyAfter(t, dst, plan)
	})
}

// Every value that does not fit its column is listed from one pass (table, column, row key and
// why), the first fifty shown and the rest counted, and nothing is committed: text with a NUL,
// text that is not UTF-8, text in an integer column, and sixty more. The mutant, named here: the
// first bad value alone reported.
func TestACopyListsEveryValueThatDoesNotFit(t *testing.T) {
	ctx := context.Background()
	dst, src := copyStores(t)
	plan := fillSource(t, dst, src)
	// event loads before preference (neither references a table; by name), so its three are listed
	for _, q := range []string{
		`UPDATE event SET kind = 'a' || char(0) || 'b' WHERE seq = 1`,
		`UPDATE event SET detail = CAST(x'fffe' AS TEXT) WHERE seq = 2`,
		`UPDATE event SET at = 'seven' WHERE seq = 3`,
	} {
		if _, err := src.w.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for i := 0; i < 60; i++ {
		if _, err := src.w.ExecContext(ctx, `INSERT INTO preference (name, value, updated_at) VALUES (?, 'x' || char(0), 0)`, fmt.Sprintf("p%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := CopySQLite(ctx, dst, src)
	var bad *BadValuesError
	if !errors.As(err, &bad) || !errors.Is(err, ErrCopyValues) {
		t.Fatalf("bad values: %v, want a BadValuesError", err)
	}
	if len(bad.Values) != copyBadShown || bad.More != 63-copyBadShown {
		t.Errorf("%d listed and %d more; want %d and %d", len(bad.Values), bad.More, copyBadShown, 63-copyBadShown)
	}
	msg := err.Error()
	for _, want := range []string{
		`event.kind (seq=1): text with a NUL byte`,
		`event.detail (seq=2): text that is not valid UTF-8`,
		`event.at (seq=3): text "seven" in an integer column`,
		`preference.value (name="p0"): text with a NUL byte`,
		fmt.Sprintf("and %d more", 63-copyBadShown),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, msg)
		}
	}
	emptyAfter(t, dst, plan)
}

// A copy streams: no batch holds more than copyBatchRows rows, nor more than copyBatchBytes and one
// row, so a table is never in memory whole; 25,000 rows go in three batches or more.
func TestACopyStreamsInBoundedBatches(t *testing.T) {
	ctx := context.Background()
	dst, src := copyStores(t)
	fillSource(t, dst, src)
	tx, err := src.w.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(10); i < 25_010; i++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO preference (name, value, updated_at) VALUES (?, ?, 0)`, fmt.Sprintf("k%d", i), strings.Repeat("x", 600)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	batches := 0
	copyBatchHook = func(table string, rows, size int) {
		if rows > copyBatchRows || size > copyBatchBytes+4096 {
			t.Errorf("%s: a batch of %d rows, %d bytes", table, rows, size)
		}
		if table == "preference" {
			batches++
		}
	}
	t.Cleanup(func() { copyBatchHook = nil })
	if _, err := CopySQLite(ctx, dst, src); err != nil {
		t.Fatal(err)
	}
	if batches < 3 {
		t.Errorf("25,003 rows of preference went in %d batches", batches)
	}
}

// A copy that runs longer than the lease keeps it, renewed throughout, and commits; one whose
// renew stops is rolled back by the fence before its commit, the destination left empty. The hold
// keeps the transaction busy, as a long copy's batches do: the writer's session ends a transaction
// left idle for twice the lease's length. The mutant, named here: no fence before the commit (the
// copy commits under a lease that lapsed).
func TestACopyLongerThanTheLease(t *testing.T) {
	ctx := context.Background()
	// a 4-second lease, renewed every 1.3 s and lost locally after 3.3 s without one: room for a
	// loaded gate host's late renew, which a shorter lease lost
	shortLease(t, 4*time.Second)
	busyFor := func(t *testing.T, q dao.Querier, d time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); {
			if err := eachRow(ctx, q, `SELECT pg_sleep(0.25)`, func(func(...any) error) error { return nil }); err != nil {
				t.Errorf("holding the copy's transaction: %v", err)
				return
			}
		}
	}
	t.Run("renewed", func(t *testing.T) {
		dst, src := copyStores(t)
		fillSource(t, dst, src)
		copyBeforeCommit = func(q dao.Querier) { busyFor(t, q, 9*time.Second) }
		t.Cleanup(func() { copyBeforeCommit = nil })
		if _, err := CopySQLite(ctx, dst, src); err != nil {
			t.Errorf("a copy held past its lease's length: %v", err)
		}
	})
	t.Run("lapsed", func(t *testing.T) {
		dst, src := copyStores(t)
		plan := fillSource(t, dst, src)
		copyBeforeCommit = func(q dao.Querier) {
			dst.pg.stop()
			<-dst.pg.done
			busyFor(t, q, 5*time.Second)
		}
		t.Cleanup(func() { copyBeforeCommit = nil })
		_, err := CopySQLite(ctx, dst, src)
		if err == nil || !strings.Contains(err.Error(), "outlasted the destination's lease") {
			t.Errorf("a copy whose lease lapsed: %v", err)
		}
		emptyAfter(t, dst, plan)
	})
}

// The tables load parents first: every table comes after each table it references.
func TestACopyLoadsParentsFirst(t *testing.T) {
	ctx := context.Background()
	dst, src := copyStores(t)
	plan, err := copyPlan(ctx, dst, src)
	if err != nil {
		t.Fatal(err)
	}
	at := map[string]int{}
	for i, tb := range plan {
		at[tb.name] = i
	}
	err = eachRow(ctx, dst.w, `SELECT c.relname, f.relname FROM pg_constraint k
		JOIN pg_class c ON c.oid = k.conrelid JOIN pg_class f ON f.oid = k.confrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace WHERE k.contype = 'f' AND n.nspname = $1`, func(scan func(...any) error) error {
		var child, parent string
		if err := scan(&child, &parent); err != nil {
			return err
		}
		if at[child] < at[parent] {
			t.Errorf("%s loads before %s, which it references", child, parent)
		}
		return nil
	}, dst.pg.loc.Schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range plan {
		for _, c := range tb.cols {
			if tb.name == "chunk" && c.name == "tsv" {
				t.Error("the plan writes chunk.tsv, which PostgreSQL generates")
			}
		}
	}
}

// A destination that loses a row as it lands (a trigger of the database's, here) fails the count
// check before the commit: the copy is rolled back, naming the table and both counts. The mutant,
// named here: the commit before the count check (the copy commits a table short of a row).
func TestACopyShortOfARowIsRolledBack(t *testing.T) {
	ctx := context.Background()
	dst, src := copyStores(t)
	plan := fillSource(t, dst, src)
	for _, q := range []string{
		`CREATE FUNCTION drop_v1() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN DELETE FROM preference WHERE name = 'v1'; RETURN NULL; END $$`,
		`CREATE TRIGGER drop_v1 AFTER INSERT ON preference FOR EACH STATEMENT EXECUTE FUNCTION drop_v1()`,
	} {
		if _, err := dst.w.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_, err := CopySQLite(ctx, dst, src)
	if !errors.Is(err, ErrCopyCheck) || !strings.Contains(err.Error(), "preference has 2 rows in the destination, 3 in the source") {
		t.Errorf("a copy short of a row: %v, want ErrCopyCheck naming preference's counts", err)
	}
	emptyAfter(t, dst, plan)
}
