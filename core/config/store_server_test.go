//go:build server

package config_test

import "testing"

// [store] reads a store on PostgreSQL: its engine, the variable its DSN is in (never the DSN), its
// schema and its key file; with no [store], the store is SQLite's.
func TestLoadReadsAPostgresStore(t *testing.T) {
	c, err := load(t, "[store]\nengine = \"postgres\"\ndsn_env = \"AUTODOC_STORE_DSN\"\nschema = \"autodoc\"\nkey_file = \"/k/store.key\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if s := c.Store; !s.Postgres() || s.DSNEnv != "AUTODOC_STORE_DSN" || s.Schema != "autodoc" || s.KeyFile != "/k/store.key" {
		t.Errorf("[store] = %+v", s)
	}
	if d, err := load(t, ""); err != nil || d.Store.Postgres() {
		t.Errorf("no [store]: %+v, %v; want SQLite's", d.Store, err)
	}
}
