package todo_test

import (
	"os"
	"path/filepath"
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
	for _, suite := range []struct {
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
	} {
		t.Run(suite.name, suite.run)
	}
}
