package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/yongjohnlee80/golib/dao/sqlite"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// seedLeftover adds model fp to workspace ws with n vectors, active as given, through a raw
// connection to the store's file. Each vector's text is a chunk's, of a document seeded with them,
// so the orphan pass (ADR 1791329335) keeps them: only the model sweep decides.
func seedLeftover(t *testing.T, db *store.Store, ws int64, fp string, n, active int) {
	t.Helper()
	seedVectors(t, db, ws, fp, n, active, true)
}

// seedVectors is seedLeftover, with the chunks only when withChunks: without them, every vector is
// an orphan.
func seedVectors(t *testing.T, db *store.Store, ws int64, fp string, n, active int, withChunks bool) {
	t.Helper()
	ctx := context.Background()
	raw, err := sqlite.OpenNamed(ctx, "seed:"+db.Path(), "file:"+db.Path()+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, "INSERT OR IGNORE INTO model (workspace_id, fp, provider, name, dims, active, target) VALUES (?, ?, 'fake', ?, 3, ?, 0)",
		ws, fp, fp, active); err != nil {
		t.Fatal(err)
	}
	var doc int64
	if withChunks {
		res, err := raw.ExecContext(ctx, "INSERT INTO document (workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (?, ?, 'v', 1, 'seed', 0)",
			ws, "seed-"+fp+".md")
		if err != nil {
			t.Fatal(err)
		}
		if doc, err = res.LastInsertId(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		hash := []byte(fmt.Sprintf("%s-%d", fp, i))
		if !withChunks {
			hash = []byte(fmt.Sprintf("%s-orphan-%d", fp, i))
		}
		if _, err := raw.ExecContext(ctx, "INSERT INTO embedding (workspace_id, text_hash, model_fp, bits, f32) VALUES (?, ?, ?, x'00', x'00000000')",
			ws, hash, fp); err != nil {
			t.Fatal(err)
		}
		if withChunks {
			if _, err := raw.ExecContext(ctx, `INSERT INTO chunk (workspace_id, doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end)
				VALUES (?, ?, ?, ?, 1, ?, '', 'seed', '', '', 0, 0)`, ws, doc, hash, hash, i); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// modelsOf lists workspace ws's models as "fp:vectors".
func modelsOf(t *testing.T, db *store.Store, ws int64) map[string]int {
	t.Helper()
	out := map[string]int{}
	err := db.Read(context.Background(), func(tx *store.Tx) error {
		ms, err := db.Workspace(ws).Models(tx).Select(store.ModelFP)
		if err != nil {
			return err
		}
		for _, m := range ms {
			es, err := db.Workspace(ws).Embeddings(tx).With(store.EmbModel, m.FP).Select(store.EmbTextHash)
			if err != nil {
				return err
			}
			out[m.FP] = len(es)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTheStartSweepReclaimsAndCompacts (ADR 1791284787 §2.3, item 3; §2.5): after the workspaces are
// open, every stored workspace keeps only its active (and target) models: one an indexer runs,
// through it, and one with no indexer, through the store. Then the store is in incremental
// auto-vacuum mode, each step said once as an event; a second start compacts nothing again.
func TestTheStartSweepReclaimsAndCompacts(t *testing.T) {
	ctx := context.Background()
	m, db := openWith(t, Options{Provider: namedProvider{name: "a"}})
	kbWith(t, m, map[string]string{"a.md": "# A\n\nwords\n"})
	modelOf(t, m, "kb", "a")
	kb := mustID(t, m, "kb")
	seedLeftover(t, db, kb, "fake|old|x|3", 3, 0)

	unserved, err := db.AddWorkspace(ctx, "elsewhere", "/nowhere", nil, nil) // stored, not served: no indexer
	if err != nil {
		t.Fatal(err)
	}
	seedLeftover(t, db, unserved.ID, "fake|kept|x|3", 2, 1)
	seedLeftover(t, db, unserved.ID, "fake|left|x|3", 4, 0)

	m.SweepModels(ctx)
	if got := modelsOf(t, db, kb); len(got) != 1 {
		t.Errorf("kb after the sweep: %v; want its active model alone", got)
	} else if _, still := got["fake|old|x|3"]; still {
		t.Errorf("kb kept its leftover: %v", got)
	}
	if got := modelsOf(t, db, unserved.ID); len(got) != 1 || got["fake|kept|x|3"] != 2 {
		t.Errorf("the unserved workspace after the sweep: %v; want its active model, with its 2 vectors", got)
	}
	if mode, _ := db.AutoVacuum(ctx); mode != store.AutoVacuumIncremental {
		t.Errorf("auto_vacuum after the start: %d, want incremental", mode)
	}
	kinds := func() map[string]int {
		evs, _, _, err := db.Events(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, e := range evs {
			out[e.Kind]++
		}
		return out
	}
	if k := kinds(); k["models.reclaimed"] != 2 || k["store.compacted"] != 1 {
		t.Errorf("events after the start: %v; want two reclaims and one compaction", k)
	}
	m.SweepModels(ctx) // a second start: nothing left, already compacted
	if k := kinds(); k["models.reclaimed"] != 2 || k["store.compacted"] != 1 {
		t.Errorf("events after a second start: %v; want nothing more", k)
	}
}

// TestTheStoreIsCompactedOnlyOnceEveryWorkspaceSwept (ADR 1791284787 §2.5): a start whose sweep of
// a workspace fails compacts nothing, so that workspace's leftovers are not left out of the one
// rewrite; the next start sweeps it, then compacts.
func TestTheStoreIsCompactedOnlyOnceEveryWorkspaceSwept(t *testing.T) {
	ctx := context.Background()
	m, db := openWith(t, Options{})
	w, err := db.AddWorkspace(ctx, "elsewhere", "/nowhere", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	seedLeftover(t, db, w.ID, "fake|left|x|3", 3, 0)
	raw, err := sqlite.OpenNamed(ctx, "fault:"+db.Path(), "file:"+db.Path()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, "CREATE TRIGGER refuse BEFORE DELETE ON embedding BEGIN SELECT RAISE(ABORT, 'injected'); END"); err != nil {
		t.Fatal(err)
	}
	m.SweepModels(ctx)
	if mode, _ := db.AutoVacuum(ctx); mode != store.AutoVacuumNone {
		t.Fatalf("compacted with a workspace's sweep failed: auto_vacuum %d", mode)
	}
	if got := modelsOf(t, db, w.ID); got["fake|left|x|3"] != 3 {
		t.Fatalf("the failed sweep: %v", got)
	}
	if _, err := raw.ExecContext(ctx, "DROP TRIGGER refuse"); err != nil {
		t.Fatal(err)
	}
	m.SweepModels(ctx) // the next start
	if got := modelsOf(t, db, w.ID); len(got) != 0 {
		t.Errorf("the next start left %v", got)
	}
	if mode, _ := db.AutoVacuum(ctx); mode != store.AutoVacuumIncremental {
		t.Errorf("the next start did not compact: auto_vacuum %d", mode)
	}
}

// TestTheStoreIsCompactedOnlyOnceEveryOrphanPassSucceeded (ADR 1791329335 §2.4, Lector r0): the
// orphan pass runs after a workspace's model sweep, and its failure defers the compaction as a failed
// model sweep does; the next start finishes the pass, then compacts.
func TestTheStoreIsCompactedOnlyOnceEveryOrphanPassSucceeded(t *testing.T) {
	ctx := context.Background()
	m, db := openWith(t, Options{})
	w, err := db.AddWorkspace(ctx, "elsewhere", "/nowhere", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	seedVectors(t, db, w.ID, "fake|kept|x|3", 2, 1, true)  // used: kept
	seedVectors(t, db, w.ID, "fake|kept|x|3", 3, 1, false) // orphans
	raw, err := sqlite.OpenNamed(ctx, "fault:"+db.Path(), "file:"+db.Path()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// the model sweep has nothing to delete; the orphan pass's deletes are refused
	if _, err := raw.ExecContext(ctx, "CREATE TRIGGER refuse BEFORE DELETE ON embedding WHEN instr(CAST(old.text_hash AS TEXT), '-orphan-') > 0 BEGIN SELECT RAISE(ABORT, 'injected'); END"); err != nil {
		t.Fatal(err)
	}
	m.SweepModels(ctx)
	if mode, _ := db.AutoVacuum(ctx); mode != store.AutoVacuumNone {
		t.Fatalf("compacted with a workspace's orphan pass failed: auto_vacuum %d", mode)
	}
	if got := modelsOf(t, db, w.ID); got["fake|kept|x|3"] != 5 {
		t.Fatalf("the failed pass: %v, want the 5 vectors", got)
	}
	if _, err := raw.ExecContext(ctx, "DROP TRIGGER refuse"); err != nil {
		t.Fatal(err)
	}
	m.SweepModels(ctx) // the next start
	if got := modelsOf(t, db, w.ID); got["fake|kept|x|3"] != 2 {
		t.Errorf("the next start: %v, want the 2 used vectors", got)
	}
	if mode, _ := db.AutoVacuum(ctx); mode != store.AutoVacuumIncremental {
		t.Errorf("the next start did not compact: auto_vacuum %d", mode)
	}
}
