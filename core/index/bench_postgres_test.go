//go:build server

package index

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// benchEnvOnPostgres is benchEnv on a scratch PostgreSQL database: n generated files, every one
// semantic-ready.
func benchEnvOnPostgres(b *testing.B, n int) *env {
	b.Helper()
	e := newEnvOnPostgres(b, options{Provider: newFake("m", "a"), BatchDelay: time.Millisecond, BatchSize: 200})
	var pc []string
	for i := range n {
		pc = append(pc, fmt.Sprintf("notes/%04d.md", i), benchNote(i))
	}
	for i := 0; i < len(pc); i += 200 {
		e.put(pc[i:min(i+200, len(pc))]...)
	}
	// ready() and atHead() read the SQLite file: here the store is read through the index's own
	st := e.ix.Store()
	e.eventually("every document semantic-ready", func() bool {
		var docs int
		unready := true
		_ = st.read(context.Background(), func(tx *store.Tx) error {
			rows, err := st.documents(tx).Select(DocID)
			docs = len(rows)
			if err == nil {
				unready, err = st.documents(tx).With(DocSemanticReady, int64(0)).Exists()
			}
			return err
		})
		return docs == n && !unready
	})
	e.eventually("the snapshot loaded", func() bool { return e.ix.sem.snap.Load() != nil })
	return e
}

// BenchmarkSearchOnPostgres is BenchmarkSearch with the store on PostgreSQL, where every statement
// of a search is a round trip; Signals is the signals' read alone, for 20 candidate documents, as
// a search reads them for its hits.
func BenchmarkSearchOnPostgres(b *testing.B) {
	e := benchEnvOnPostgres(b, 2000)
	ctx := context.Background()
	for _, m := range []struct{ name, mode string }{{"Lexical", ModeLexical}, {"Semantic", ModeSemantic}, {"Hybrid", ModeAuto}} {
		b.Run(m.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := e.ix.Search(ctx, "storage vector snapshot", QueryOpts{Mode: m.mode}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	st := searchStore{s: e.ix.Store()}
	var ids []int64
	if err := st.View(ctx, func(v *View) error {
		rows, err := v.s.documents(v.tx).Limit(20).Select(DocID)
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return err
	}); err != nil || len(ids) != 20 {
		b.Fatalf("20 documents: %d, %v", len(ids), err)
	}
	b.Run("Signals20", func(b *testing.B) {
		for b.Loop() {
			if err := st.View(ctx, func(v *View) error { _, err := v.Signals(ctx, ids); return err }); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestSignalsAreTwoReadsOnPostgres: the signals cell on PostgreSQL, where each read is a round trip.
func TestSignalsAreTwoReadsOnPostgres(t *testing.T) {
	signalsInTwoReads(t, newEnvOnPostgres(t, options{}))
}

// TestSignalsBoundByTheirInLinksOnPostgres and TestRerankerTextsAreTwoReadsOnPostgres: the same
// cells on PostgreSQL.
func TestSignalsBoundByTheirInLinksOnPostgres(t *testing.T) {
	manyInLinks(t, newEnvOnPostgres(t, options{}))
}

func TestRerankerTextsAreTwoReadsOnPostgres(t *testing.T) {
	chunkTextsInTwoReads(t, newEnvOnPostgres(t, options{}))
}
