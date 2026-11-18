package ingest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// TestMeasureADeletesLatency measures, AUTODOC_MEASURE_DELETES=1 (a separate run), how long a row
// deleted outright takes to leave search with the defaults (a pass every minute, a minute's lag):
// Run on the database's own clock, a tombstone written with now(), and the document's word looked
// for by words every second until it is gone. The bound is the interval and the lag, two minutes.
func TestMeasureADeletesLatency(t *testing.T) {
	if os.Getenv("AUTODOC_MEASURE_DELETES") != "1" {
		t.Skip("AUTODOC_MEASURE_DELETES is not set: the measurement is a separate run")
	}
	f, src := deletesFixture(t, "")
	f.track(1, "A", "zanzibar", 1)
	in := f.ingester(src, Options{Now: func(ctx context.Context) (time.Time, error) { return pgstore.Now(ctx, f.business) }}, "")
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, 3*time.Minute, "the track indexed", func() bool { return len(f.lexical("zanzibar")) == 1 })
	f.exec(`DELETE FROM track WHERE id = 1`)
	f.exec(`INSERT INTO track_deleted VALUES (1, now())`)
	start := time.Now()
	waitFor(t, 4*time.Minute, "the deleted track gone from search", func() bool {
		time.Sleep(time.Second)
		return len(f.lexical("zanzibar")) == 0
	})
	t.Logf("a row deleted outright left search %s after its tombstone, with interval %s and lag %s",
		time.Since(start).Round(time.Second), DefaultInterval, DefaultLag)
}
