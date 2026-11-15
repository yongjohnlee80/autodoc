//go:build !server

package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/config"
)

// A build without the server tag has no PostgreSQL store: a valid [store] engine = "postgres" is
// refused at load, naming the server build; a SQLite store loads.
func TestAnUntaggedBuildRefusesAPostgresStore(t *testing.T) {
	_, err := load(t, "[store]\nengine = \"postgres\"\ndsn_env = \"AUTODOC_STORE_DSN\"\nschema = \"autodoc\"\nkey_file = \"/k/store.key\"\n")
	if !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), "needs the server build (-tags server)") {
		t.Errorf("a PostgreSQL store without the server tag: %v, want ErrInvalid naming the server build", err)
	}
	if d, err := load(t, "[store]\nengine = \"sqlite\"\n"); err != nil || d.Store.Postgres() {
		t.Errorf("a SQLite store: %+v, %v", d.Store, err)
	}
}
