package pgstore

import (
	"context"
	"testing"
)

// The key is v0.3.15's, unchanged: for schema "search" and tenant "music", 3983377607322651391.
// OlderWriterHeld says false while no session holds it, true while one does, and keeps nothing.
// The mutants, named here: another key; a session lock in place of the transaction's.
func TestOlderWriterHeldTriesV0315sLock(t *testing.T) {
	if k := olderKey("search", "music"); k != 3983377607322651391 {
		t.Fatalf("olderKey(search, music) = %d; want v0.3.15's 3983377607322651391", k)
	}
	ctx := context.Background()
	conn := scratch(t)
	held := func() bool {
		t.Helper()
		h, err := OlderWriterHeld(ctx, conn, "search", "music")
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	locks := func() string {
		t.Helper()
		rows, err := conn.QueryContext(ctx, `SELECT count(*)::text FROM pg_locks WHERE locktype = 'advisory'
			AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`)
		if err != nil {
			t.Fatal(err)
		}
		var n string
		if err := scanRows(rows, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for range 3 {
		if held() {
			t.Fatal("held with no older server")
		}
	}
	if n := locks(); n != "0" {
		t.Fatalf("%s advisory locks after three checks; want none kept", n)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_lock($1)", int64(3983377607322651391)); err != nil {
		t.Fatal(err)
	}
	if !held() {
		t.Error("not held while a session holds v0.3.15's lock")
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", int64(3983377607322651391)); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if held() {
		t.Error("held after the session let it go")
	}
}
