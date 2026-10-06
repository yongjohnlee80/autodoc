package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/yongjohnlee80/golib/dao/sqlite"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// seedLeftover adds model fp to workspace ws with n vectors, active as given, through a raw
// connection to the store's file.
func seedLeftover(t *testing.T, db *store.Store, ws int64, fp string, n, active int) {
	t.Helper()
	ctx := context.Background()
	raw, err := sqlite.OpenNamed(ctx, "seed:"+db.Path(), "file:"+db.Path()+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, "INSERT INTO model (workspace_id, fp, provider, name, dims, active, target) VALUES (?, ?, 'fake', ?, 3, ?, 0)",
		ws, fp, fp, active); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := raw.ExecContext(ctx, "INSERT INTO embedding (workspace_id, text_hash, model_fp, bits, f32) VALUES (?, ?, ?, x'00', x'00000000')",
			ws, []byte(fmt.Sprintf("%s-%d", fp, i)), fp); err != nil {
			t.Fatal(err)
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
