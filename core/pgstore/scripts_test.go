package pgstore

import (
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
)

// released is every released script's digest, by engine and name, for the destination's script
// set. A released script NEVER changes (docs/ops/schema-scripts.md): the ledger records the
// digest it was applied with, and an edit under the same name is a schema no store agrees with.
// A script adds its line here when it ships in a tagged release; an existing line never changes.
// The baseline ships for the first time with this package, so its digest is recorded at that
// release.
var released = map[string]map[string]string{
	dao.DialectPostgres: {},
}

// Every script in the embedded set loads, splits into statements, and has the baseline first.
func TestTheScriptSetLoads(t *testing.T) {
	all, err := deploy.Load(migrations, dao.DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 || all[0].Name != "000001_update_initialize_tables.sql" {
		t.Fatalf("the baseline is not first: %v", all)
	}
	for _, s := range all {
		stmts, err := s.Statements()
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
		}
		if s.Number == 1 && len(stmts) == 0 {
			t.Errorf("%s: no statements", s.Name)
		}
	}
}

// A released script is immutable: its digest is the one it shipped with.
func TestReleasedScriptsAreUnchanged(t *testing.T) {
	all, err := deploy.Load(migrations, dao.DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]deploy.Script{}
	for _, s := range all {
		got[s.Name] = s
	}
	for name, digest := range released[dao.DialectPostgres] {
		s, ok := got[name]
		switch {
		case !ok:
			t.Errorf("%s was released and is gone", name)
		case digest == "":
			t.Errorf("%s: record its released digest here: %q", name, s.SHA256)
		case s.SHA256 != digest:
			t.Errorf("%s changed after release: digest %s, released %s", name, s.SHA256, digest)
		}
	}
}
