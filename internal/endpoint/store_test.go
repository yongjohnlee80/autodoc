package endpoint

import (
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// A daemon says which store it serves, and a client of a store on PostgreSQL takes it only when it
// says that engine and that schema; otherwise the refusal names both stores.
func TestADaemonForAnotherStoreIsRefusedNamingBoth(t *testing.T) {
	if err := CheckPostgresStore("sock", "autodoc", store.EngineOfPostgres, "autodoc"); err != nil {
		t.Errorf("the same schema: %v", err)
	}
	for _, c := range []struct{ engine, schema, served string }{
		{store.EngineOfPostgres, "other", "the PostgreSQL store in schema other"},
		{"", "", "a SQLite store"},
	} {
		err := CheckPostgresStore("sock", "autodoc", c.engine, c.schema)
		var m *StoreMismatchError
		if !errors.As(err, &m) || !strings.Contains(err.Error(), c.served) || !strings.Contains(err.Error(), "the PostgreSQL store in schema autodoc") {
			t.Errorf("a daemon serving %s: %v, want a mismatch naming both", c.served, err)
		}
	}
}
