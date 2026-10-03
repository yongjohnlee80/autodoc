package store

import (
	"context"
	"errors"
	"testing"
)

func TestEventLog(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if evs, cursor, more, err := s.Events(ctx, 0, 10); err != nil || len(evs) != 0 || cursor != 0 || more {
		t.Fatalf("an empty log = %v %d %v %v", evs, cursor, more, err)
	}
	for i, k := range []string{"workspace.patterns", "embedding.switched", "workspace.schema"} {
		seq, err := s.AppendEvent(ctx, Event{Kind: k, Workspace: "kb", Client: "tui-1", Detail: "d"})
		if err != nil || seq != int64(i+1) {
			t.Fatalf("append %d = %d, %v", i, seq, err)
		}
	}
	evs, cursor, more, err := s.Events(ctx, 0, 2)
	if err != nil || len(evs) != 2 || cursor != 2 || !more || evs[0].Kind != "workspace.patterns" || evs[0].Client != "tui-1" || evs[0].At == 0 {
		t.Fatalf("first page = %+v %d %v %v", evs, cursor, more, err)
	}
	evs, cursor, more, err = s.Events(ctx, cursor, 2)
	if err != nil || len(evs) != 1 || cursor != 3 || more || evs[0].Kind != "workspace.schema" {
		t.Fatalf("second page = %+v %d %v %v", evs, cursor, more, err)
	}
	if evs, cursor, _, err := s.Events(ctx, -1, 10); err != nil || len(evs) != 0 || cursor != 3 {
		t.Fatalf("the head = %v %d %v", evs, cursor, err)
	}
	if _, _, _, err := s.Events(ctx, 4, 10); !errors.Is(err, ErrEventsExpired) {
		t.Fatalf("a cursor past the head: %v", err)
	}
}

// The log keeps EventsKept rows; a cursor older than them expires, the one just before the oldest
// does not, and seq keeps rising.
func TestEventLogPrunes(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	for range EventsKept + 5 {
		if _, err := s.AppendEvent(ctx, Event{Kind: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	evs, cursor, _, err := s.Events(ctx, 5, EventsKept+10)
	if err != nil || len(evs) != EventsKept || evs[0].Seq != 6 || cursor != EventsKept+5 {
		t.Fatalf("from the oldest-1: %d events from %v, cursor %d, %v", len(evs), evs[0].Seq, cursor, err)
	}
	if _, _, _, err := s.Events(ctx, 4, 10); !errors.Is(err, ErrEventsExpired) {
		t.Fatalf("a pruned cursor: %v", err)
	}
	if seq, _ := s.AppendEvent(ctx, Event{Kind: "k"}); seq != EventsKept+6 {
		t.Fatalf("seq after pruning = %d", seq)
	}
}
