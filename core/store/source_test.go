package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Data access is the store's: outside core/store no production code opens a SQL connection, runs
// SQL of its own, or batches without the scope's workspace stamp. Every read and write elsewhere
// goes through a Scope's DAOs (and dao's predicates), or a package's own tables declared through
// the store's seam: a file that declares with store.Declare may build its tables (dao.New), and
// nothing more.
//
// core/pgstore is exempt: it is a store itself — a search.Store over a destination database, the
// one package beside core/store that owns dao declarations — and its every statement carries its
// tenant, the same rule this test holds the rest of the tree to. core/pglease is exempt too: it is
// the stores' writer lease, a row each store keeps in its own database (pgstore's, and core/store's
// on PostgreSQL), and its statements read and write that row alone, by its scope, in the one table
// its caller names, which it checks is a plain identifier before any I/O.
func TestNoDataAccessOutsideTheStore(t *testing.T) {
	root := filepath.Join("..", "..")
	forbidden := []string{
		`"github.com/yongjohnlee80/golib/dao/sqlite"`, // a connection of its own
		"dao.New(",      // a table declaration
		"dao.RunTx(",    // a transaction around no Scope
		"dao.Raw(",      // SQL as a predicate
		"dao.SQL(",      // SQL as an expression
		"ExecContext(",  // a statement
		"QueryContext(", // a query
		".Batch()",      // a batch without the workspace stamp (use a Scope's …Batch)
	}
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", ".autodoc-test-logs":
				return filepath.SkipDir
			}
			skip := filepath.Join(root, "core", "store")
			if filepath.ToSlash(path) == filepath.ToSlash(skip) {
				return filepath.SkipDir
			}
			if filepath.ToSlash(path) == filepath.ToSlash(filepath.Join(root, "core", "pgstore")) ||
				filepath.ToSlash(path) == filepath.ToSlash(filepath.Join(root, "core", "pglease")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		declares := strings.Contains(string(b), "store.Declare(")
		for _, f := range forbidden {
			if f == "dao.New(" && declares {
				continue // its tables, declared through the seam (declare.go)
			}
			if strings.Contains(string(b), f) {
				t.Errorf("%s: %s outside core/store", path, f)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 30 {
		t.Fatalf("only %d files checked: this test would show nothing", checked)
	}
}
