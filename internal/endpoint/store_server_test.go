//go:build server

package endpoint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/store"
)

func endpointsOf(t *testing.T, body string) *Endpoints {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// A store on PostgreSQL's location: the DSN read from the variable store.dsn_env names, the key in
// the data directory unless store.key_file names one; a variable not set is refused, naming it. A
// SQLite store's location is its file. Paths carry the engine and schema, never the DSN.
func TestTheStoresLocationReadsItsDSNFromItsVariable(t *testing.T) {
	data := t.TempDir()
	e := endpointsOf(t, "[server]\ndata_dir = \""+filepath.ToSlash(data)+"\"\n[store]\nengine = \"postgres\"\ndsn_env = \"AUTODOC_TEST_LOC_DSN\"\nschema = \"autodoc\"\n")
	t.Setenv("AUTODOC_TEST_LOC_DSN", "")
	if _, err := e.StoreLocation(); !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), "$AUTODOC_TEST_LOC_DSN") {
		t.Errorf("a DSN variable not set: %v, want ErrInvalid naming it", err)
	}
	t.Setenv("AUTODOC_TEST_LOC_DSN", "postgres://u:secret@h/db")
	loc, err := e.StoreLocation()
	if err != nil {
		t.Fatal(err)
	}
	if loc.Engine != store.EngineOfPostgres || loc.DSN != "postgres://u:secret@h/db" || loc.Schema != "autodoc" || loc.KeyFile != filepath.Join(data, config.StoreKeyName) {
		t.Errorf("the location: %+v", loc)
	}
	p, err := e.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if !p.Postgres() || p.Schema != "autodoc" || strings.Contains(p.Store+p.Socket+p.StateDir, "secret") {
		t.Errorf("the paths: %+v, want the engine and schema, never the DSN", p)
	}
	s := endpointsOf(t, "[server]\ndata_dir = \""+filepath.ToSlash(data)+"\"\n")
	if loc, err := s.StoreLocation(); err != nil || loc.Engine != store.EngineOfSQLite || loc.Path != filepath.Join(data, config.StoreName) {
		t.Errorf("a SQLite store's location: %+v, %v", loc, err)
	}
}
