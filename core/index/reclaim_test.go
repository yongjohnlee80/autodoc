package index

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// vectorsOf counts model fp's vectors, and whether its row is there.
func (e *env) vectorsOf(fp string) (vectors int, row bool) {
	e.t.Helper()
	var rows int
	_ = scanOne(context.Background(), e.raw, &vectors, "SELECT COUNT(*) FROM embedding WHERE model_fp = ?", fp)
	_ = scanOne(context.Background(), e.raw, &rows, "SELECT COUNT(*) FROM model WHERE fp = ?", fp)
	return vectors, rows == 1
}

// seedVectors adds n vectors under the existing model fp, as a part-way fill would have.
func (e *env) seedVectors(fp string, n int) {
	e.t.Helper()
	for i := 0; i < n; i++ {
		if _, err := e.raw.ExecContext(context.Background(), "INSERT INTO embedding (workspace_id, text_hash, model_fp, bits, f32) VALUES (?, ?, ?, x'00', x'00000000')",
			e.ws, []byte(fmt.Sprintf("p%04d", i)), fp); err != nil {
			e.t.Fatal(err)
		}
	}
}

// seedModel adds model fp to workspace ws, with n vectors, active and target as given.
func (e *env) seedModel(ws int64, fp string, n int, active, target int) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.raw.ExecContext(ctx, "INSERT INTO model (workspace_id, fp, provider, name, dims, active, target) VALUES (?, ?, 'fake', ?, 4, ?, ?)",
		ws, fp, fp, active, target); err != nil {
		e.t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := e.raw.ExecContext(ctx, "INSERT INTO embedding (workspace_id, text_hash, model_fp, bits, f32) VALUES (?, ?, ?, x'00', x'00000000')",
			ws, []byte(fmt.Sprintf("h%04d", i)), fp); err != nil {
			e.t.Fatal(err)
		}
	}
}

// TestACancelledSwitchIsReclaimed (ADR 1791284787 §2.3, item 2): a switch to b, part-way, goes back
// to a: b's partial vectors and its row go, and a, which answered throughout, keeps every vector.
func TestACancelledSwitchIsReclaimed(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n", "y.md", "hippo\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	kept, _ := e.vectorsOf(fpA)

	b := newFake("m2", "b")
	b.hold("hippo") // b's first batch waits: the switch is under way
	e.open(Options{Provider: b})
	fpB := b.Model().Fingerprint()
	e.eventually("b filling", func() bool { return len(b.texts()) > 0 })
	e.seedVectors(fpB, 3) // what b had embedded so far
	if e.activeModel() != fpA {
		t.Fatalf("a part-filled b flipped: active %s", e.activeModel())
	}

	e.open(Options{Provider: a}) // the cancel: the provider goes back to a
	b.unhold()
	e.eventually("b reclaimed", func() bool { n, row := e.vectorsOf(fpB); return n == 0 && !row })
	if n, row := e.vectorsOf(fpA); n != kept || !row || e.activeModel() != fpA {
		t.Errorf("a after the cancel: %d of %d vectors, row %v, active %s", n, kept, row, e.activeModel())
	}
}

// TestARetargetedSwitchReclaimsTheOneLeft: a active, b filling, then c: b is reclaimed, a is
// untouched, and c is the target.
func TestARetargetedSwitchReclaimsTheOneLeft(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n", "y.md", "hippo\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	kept, _ := e.vectorsOf(fpA)
	b := newFake("m2", "b")
	b.hold("hippo")
	e.open(Options{Provider: b})
	fpB := b.Model().Fingerprint()
	e.eventually("b filling", func() bool { return len(b.texts()) > 0 })
	e.seedVectors(fpB, 3)
	c := newFake("m3", "c")
	c.hold("hippo")
	e.open(Options{Provider: c})
	b.unhold()
	e.eventually("b reclaimed", func() bool { n, row := e.vectorsOf(fpB); return n == 0 && !row })
	var target string
	_ = scanOne(context.Background(), e.raw, &target, "SELECT fp FROM model WHERE target = 1")
	if n, _ := e.vectorsOf(fpA); n != kept || e.activeModel() != fpA || target != c.Model().Fingerprint() {
		t.Errorf("a: %d of %d vectors, active %s, target %s", n, kept, e.activeModel(), target)
	}
	c.unhold()
}

// TestASweepKeepsTheActiveAndTheTarget (§2.3, item 3): a workspace's indexer sweeps its leftovers
// and keeps what it answers with; a workspace with no indexer is swept by the store alone, keeping
// its recorded active model and target.
func TestASweepKeepsTheActiveAndTheTarget(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	e.seedModel(e.ws, "old|one", 3, 0, 0)
	e.seedModel(e.ws, "old|two", 2, 0, 0)
	got, err := e.ix.Sweep(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("Sweep: %+v, %v", got, err)
	}
	for _, fp := range []string{"old|one", "old|two"} {
		if n, row := e.vectorsOf(fp); n != 0 || row {
			t.Errorf("%s kept %d vectors, row %v", fp, n, row)
		}
	}
	if n, row := e.vectorsOf(fpA); n == 0 || !row {
		t.Errorf("the active model lost its vectors: %d, row %v", n, row)
	}

	w2, err := e.db.AddWorkspace(context.Background(), "other", "/other", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.seedModel(w2.ID, "w2|active", 3, 1, 0)
	e.seedModel(w2.ID, "w2|target", 2, 0, 1)
	e.seedModel(w2.ID, "w2|left", 4, 0, 0)
	if got, err := SweepStore(context.Background(), e.db, w2.ID); err != nil || len(got) != 1 || got[0].FP != "w2|left" || !got[0].Gone || got[0].Vectors != 4 {
		t.Fatalf("SweepStore: %+v, %v", got, err)
	}
	for fp, want := range map[string]int{"w2|active": 3, "w2|target": 2} {
		if n, row := e.vectorsOf(fp); n != want || !row {
			t.Errorf("%s: %d vectors, row %v; want %d and its row", fp, n, row, want)
		}
	}
}

// TestAReclaimTakesBatches (§2.2): a model's vectors go a batch at a time, each its own write,
// then its row in a write of its own; a write by anyone else can commit between them.
func TestAReclaimTakesBatches(t *testing.T) {
	was := reclaimBatch
	reclaimBatch = 2
	t.Cleanup(func() { reclaimBatch = was })
	e := newEnv(t, Options{})
	e.seedModel(e.ws, "old|m", 5, 0, 0)
	writes := 0
	counting := func(ctx context.Context, fn func(context.Context, *store.Tx) error) error {
		writes++
		if writes == 2 { // another writer's turn, between two batches
			e.put("between.md", "written between the batches\n")
		}
		return e.store.storeWrite(ctx, fn)
	}
	r, err := e.store.reclaim(context.Background(), "old|m", counting)
	if err != nil || r.Vectors != 5 || !r.Gone {
		t.Fatalf("reclaim: %+v, %v", r, err)
	}
	if writes != 4 { // 2 + 2 + 1 vectors, then the row
		t.Errorf("%d writes, want 4: three batches of at most 2, then the row", writes)
	}
	if _, ok := e.store.Version("between.md"); !ok {
		t.Error("the write between the batches did not commit")
	}
}

// TestAReclaimLeavesAModelSwitchedBack (§2.2): a model made the target again while it is being
// reclaimed keeps what is left: the next batch finds it in use, and stops.
func TestAReclaimLeavesAModelSwitchedBack(t *testing.T) {
	was := reclaimBatch
	reclaimBatch = 2
	t.Cleanup(func() { reclaimBatch = was })
	e := newEnv(t, Options{})
	e.seedModel(e.ws, "back|m", 5, 0, 0)
	writes := 0
	racing := func(ctx context.Context, fn func(context.Context, *store.Tx) error) error {
		writes++
		if writes == 2 { // the switch back, after the first batch
			if _, err := e.raw.ExecContext(ctx, "UPDATE model SET target = 1 WHERE fp = 'back|m'"); err != nil {
				return err
			}
		}
		return e.store.storeWrite(ctx, fn)
	}
	r, err := e.store.reclaim(context.Background(), "back|m", racing)
	if err != nil || r.Gone || r.Vectors != 2 {
		t.Fatalf("reclaim of a model switched back: %+v, %v; want 2 vectors gone and the rest kept", r, err)
	}
	if n, row := e.vectorsOf("back|m"); n != 3 || !row {
		t.Errorf("the model switched back has %d vectors, row %v; want 3 and its row", n, row)
	}
}

// TestASweepFinishesAReclaimCutShort (§2.2, restart-safety): a reclaim that fails part-way leaves an
// inactive model with fewer vectors and its row; the next sweep finishes it.
func TestASweepFinishesAReclaimCutShort(t *testing.T) {
	was := reclaimBatch
	reclaimBatch = 2
	t.Cleanup(func() { reclaimBatch = was })
	e := newEnv(t, Options{})
	e.seedModel(e.ws, "cut|m", 5, 0, 0)
	writes := 0
	crashing := func(ctx context.Context, fn func(context.Context, *store.Tx) error) error {
		writes++
		if writes == 2 {
			return errors.New("the daemon stopped")
		}
		return e.store.storeWrite(ctx, fn)
	}
	if _, err := e.store.reclaim(context.Background(), "cut|m", crashing); err == nil {
		t.Fatal("the cut-short reclaim reported no error")
	}
	if n, row := e.vectorsOf("cut|m"); n != 3 || !row {
		t.Fatalf("after the cut: %d vectors, row %v; want 3 and the row", n, row)
	}
	if got, err := SweepStore(context.Background(), e.db, e.ws); err != nil || len(got) != 1 || !got[0].Gone {
		t.Fatalf("the sweep after it: %+v, %v", got, err)
	}
	if n, row := e.vectorsOf("cut|m"); n != 0 || row {
		t.Errorf("the sweep left %d vectors, row %v", n, row)
	}
}

// TestAReadOpenAcrossTheFlipKeepsTheOldVectors (§2.3, item 1): a read transaction opened before a
// switch's flip still reads the superseded model's vectors after the reclaim has deleted them: its
// snapshot is its own, and the reclaim comes after the publish.
func TestAReadOpenAcrossTheFlipKeepsTheOldVectors(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n", "y.md", "hippo\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	ctx := context.Background()
	tx, err := e.raw.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var before int
	if err := scanOne(ctx, tx, &before, "SELECT COUNT(*) FROM embedding WHERE model_fp = ?", fpA); err != nil || before == 0 {
		t.Fatalf("the read before the flip: %d, %v", before, err)
	}
	b := newFake("m2", "b")
	e.open(Options{Provider: b})
	e.eventually("the flip, then the reclaim of a", func() bool { n, row := e.vectorsOf(fpA); return n == 0 && !row })
	var still int
	if err := scanOne(ctx, tx, &still, "SELECT COUNT(*) FROM embedding WHERE model_fp = ?", fpA); err != nil || still != before {
		t.Errorf("the read opened before the flip now sees %d of a's %d vectors (%v)", still, before, err)
	}
}

// TestPurgeSaysWhatToDoInstead (§2.4): purging the active model is refused, pointing at AI models,
// and deletes nothing; an inactive model is reclaimed at once.
func TestPurgeSaysWhatToDoInstead(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	kept, _ := e.vectorsOf(fpA)
	err := e.ix.PurgeModel(context.Background(), fpA)
	if !errors.Is(err, ErrModelInUse) || !strings.Contains(err.Error(), "m is the active model: choose another model, or remove its provider in AI models") {
		t.Errorf("purging the active model: %v", err)
	}
	if n, row := e.vectorsOf(fpA); n != kept || !row {
		t.Errorf("a refused purge deleted: %d of %d vectors, row %v", n, kept, row)
	}
	// a switch to b under way: b is the target, and purging it is the cancel's to do
	b := newFake("m2", "b")
	b.hold("zebra")
	e.open(Options{Provider: b})
	fpB := b.Model().Fingerprint()
	e.eventually("b filling", func() bool { return len(b.texts()) > 0 })
	err = e.ix.PurgeModel(context.Background(), fpB)
	if !errors.Is(err, ErrModelInUse) || !strings.Contains(err.Error(), "m2 is the switch's target: cancel the switch") {
		t.Errorf("purging the target: %v", err)
	}
	b.unhold()
	e.seedModel(e.ws, "fake|old|sha256:o|4", 3, 0, 0)
	if err := e.ix.PurgeModel(context.Background(), "fake|old|sha256:o|4"); err != nil {
		t.Fatal(err)
	}
	if n, row := e.vectorsOf("fake|old|sha256:o|4"); n != 0 || row {
		t.Errorf("a purged inactive model kept %d vectors, row %v", n, row)
	}
}

// TestAFailedTargetWriteChangesNothing: setupModels records the target in its own transaction; a
// write refused part-way (clearing the old target, or setting the new) fails it whole, and the
// target is as it was.
func TestAFailedTargetWriteChangesNothing(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	target := func() string {
		var fp string
		_ = scanOne(context.Background(), e.raw, &fp, "SELECT fp FROM model WHERE target = 1")
		return fp
	}
	for _, trigger := range []string{
		"CREATE TRIGGER refuse BEFORE UPDATE OF target ON model WHEN OLD.target = 1 AND NEW.target = 0 BEGIN SELECT RAISE(ABORT, 'injected'); END",
		"CREATE TRIGGER refuse BEFORE UPDATE OF target ON model WHEN OLD.target = 0 AND NEW.target = 1 BEGIN SELECT RAISE(ABORT, 'injected'); END",
	} {
		if _, err := e.raw.ExecContext(context.Background(), trigger); err != nil {
			t.Fatal(err)
		}
		e.ix.sem.target = newFake("m2", "b") // the indexer asked to fill another model
		if err := e.ix.setupModels(context.Background()); err == nil {
			t.Errorf("setupModels under %q: no error", trigger)
		}
		if got := target(); got != fpA {
			t.Errorf("after a refused write the target is %q, want a's", got)
		}
		e.ix.sem.target = a
		if _, err := e.raw.ExecContext(context.Background(), "DROP TRIGGER refuse"); err != nil {
			t.Fatal(err)
		}
	}
}
