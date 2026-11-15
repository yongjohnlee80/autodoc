package store

import (
	"context"
	"os"
	"testing"
)

// storeEngineEnv picks the engine openStore opens a store on: "postgres" for a scratch PostgreSQL
// database (AUTODOC_TEST_PGURL, the server build's: -tags server), SQLite otherwise.
const storeEngineEnv = "AUTODOC_TEST_STORE_ENGINE"

// sqliteOnly skips a cell of what only a SQLite store's file has (why says what), when openStore
// opens its stores on PostgreSQL.
func sqliteOnly(t *testing.T, why string) {
	t.Helper()
	if os.Getenv(storeEngineEnv) == EngineOfPostgres {
		t.Skip("a SQLite store's own: " + why)
	}
}

func openPG(t *testing.T, loc Location) *Store {
	t.Helper()
	s, err := OpenAt(context.Background(), loc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
