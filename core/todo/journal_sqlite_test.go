package todo_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// newJournal is the journal an env runs on: the in-memory one, but for the run below.
var newJournal = func(*testing.T) todo.Journal { return todotest.NewJournal() }

// Every suite that runs on an env runs again on the journal the daemon keeps: core/store's SQLite
// one, a store file per env. The crash suite among them stops each mutation at every step and
// recovers from what the database holds. A new env suite joins the list.
func TestTheSuitesOnTheSQLiteJournal(t *testing.T) {
	// a store's schema is applied once, and each env opens a copy: applying it is most of an open
	template := filepath.Join(t.TempDir(), "autodoc.db")
	s, err := store.Open(ctx, template)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(template)
	if err != nil {
		t.Fatal(err)
	}
	inMemory := newJournal
	newJournal = func(t *testing.T) todo.Journal {
		path := filepath.Join(t.TempDir(), "autodoc.db")
		if err := os.WriteFile(path, schema, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := store.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s.TodoJournal()
	}
	t.Cleanup(func() { newJournal = inMemory })
	for _, suite := range sqliteSuites {
		t.Run(suite.name, suite.run)
	}
}

// sqliteSuites are the env suites, every one of them: TestEveryEnvSuiteRunsOnSQLite keeps the list
// whole.
var sqliteSuites = []struct {
	name string
	run  func(*testing.T)
}{
	{"EveryVerbWritesItsRow", TestEveryVerbWritesItsRow},
	{"HandEditsAreUpdatesNeverAssigns", TestHandEditsAreUpdatesNeverAssigns},
	{"FirstSightIsABaseline", TestFirstSightIsABaseline},
	{"Consumers", TestConsumers},
	{"PruningAndResync", TestPruningAndResync},
	{"AddWritesAutoCoresFile", TestAddWritesAutoCoresFile},
	{"TransitionsAnyToAny", TestTransitionsAnyToAny},
	{"AssignD1", TestAssignD1},
	{"UpdatePatchesHandEditableFieldsOnly", TestUpdatePatchesHandEditableFieldsOnly},
	{"StaleRevIsRefused", TestStaleRevIsRefused},
	{"HandEditBetweenReadAndWriteIsCaught", TestHandEditBetweenReadAndWriteIsCaught},
	{"RacingWritesAreSerialized", TestRacingWritesAreSerialized},
	{"RefreshRules", TestRefreshRules},
	{"MalformedFilesAreListedAndRemovedOnRequest", TestMalformedFilesAreListedAndRemovedOnRequest},
	{"RefreshResolvesAnInterruptedMovesDuplicate", TestRefreshResolvesAnInterruptedMovesDuplicate},
	{"ListFilters", TestListFilters},
	{"Vars", TestVars},
	{"RecoveryAfterACrashAtEveryStep", TestRecoveryAfterACrashAtEveryStep},
	{"RecoveryCallsAForeignChangeAmbiguous", TestRecoveryCallsAForeignChangeAmbiguous},
	{"RecoveryOfAForeignRemoval", TestRecoveryOfAForeignRemoval},
	{"AnAmbiguousStoreMoveBlocksWrites", TestAnAmbiguousStoreMoveBlocksWrites},
	{"WritesWaitForTheClaim", TestWritesWaitForTheClaim},
	{"NewProjectOrder", TestNewProjectOrder},
	{"Where", TestWhere},
	{"Confinement", TestConfinement},
	{"MoveStoreKeepsTheStore", TestMoveStoreKeepsTheStore},
	{"ArchivesAreReadOnRequestOnly", TestArchivesAreReadOnRequestOnly},
	{"ArchivesAtScale", TestArchivesAtScale},
	{"ReadingNeverWrites", TestReadingNeverWrites},
	{"AddWithAnAssigneeIsAnAssignment", TestAddWithAnAssigneeIsAnAssignment},
	{"ClaimRewritesLegacyWorkspaceRefs", TestClaimRewritesLegacyWorkspaceRefs},
	{"TheFirstWriteKeepsTheBaseline", TestTheFirstWriteKeepsTheBaseline},
	{"TheVerbsServeTheManager", TestTheVerbsServeTheManager},
	{"EveryVerbAnswers", TestEveryVerbAnswers},
	{"ScaffoldAnEmptyOverride", TestScaffoldAnEmptyOverride},
}

// TestEveryEnvSuiteRunsOnSQLite: a test that builds an env (newEnv, claimed) is in sqliteSuites, so
// none runs on the in-memory journal alone.
func TestEveryEnvSuiteRunsOnSQLite(t *testing.T) {
	listed := map[string]bool{}
	for _, s := range sqliteSuites {
		listed["Test"+s.name] = true
	}
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			usesEnv := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if id, ok := c.Fun.(*ast.Ident); ok && (id.Name == "newEnv" || id.Name == "claimed") {
						usesEnv = true
					}
				}
				return !usesEnv
			})
			if usesEnv && !listed[fn.Name.Name] {
				t.Errorf("%s (%s) builds an env but is not in sqliteSuites", fn.Name.Name, f)
			}
		}
	}
}
