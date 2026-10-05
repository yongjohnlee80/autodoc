package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// callMeter holds the calls a ranker's meter heard and writes them to the store every flushEvery,
// as the Embedding does its provider's: a write per call would take the store's one writer from the
// indexers as often as they search.
type callMeter struct {
	record func(ctx context.Context, id int64, calls []store.CallRecord) error
	log    logger.Logger
	what   string

	mu      sync.Mutex
	pending map[int64][]store.CallRecord
	unkept  int
	done    chan struct{}
}

func newCallMeter(record func(context.Context, int64, []store.CallRecord) error, log logger.Logger, what string) *callMeter {
	return &callMeter{record: record, log: log, what: what, pending: map[int64][]store.CallRecord{}, done: make(chan struct{})}
}

// hear holds a call for the store, never waiting on it.
func (m *callMeter) hear(id int64, texts, tokens int, took time.Duration, err error) {
	rec := store.CallRecord{At: time.Now(), Texts: texts, Tokens: tokens, Millis: took.Milliseconds(), Outcome: "ok"}
	if err != nil {
		rec.Failed, rec.Outcome = true, rankOutcome(err)
		rec.Limited = errors.Is(err, rank.ErrRateLimited)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending[id]) >= maxPending {
		m.unkept++
		return
	}
	m.pending[id] = append(m.pending[id], rec)
}

// flush writes the calls heard every flushEvery, and once more when ctx ends.
func (m *callMeter) flush(ctx context.Context) {
	defer close(m.done)
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			m.write(ctx)
		case <-ctx.Done():
			m.write(context.WithoutCancel(ctx))
			return
		}
	}
}

// wait returns once flush has written the last calls, after its ctx ended.
func (m *callMeter) wait() { <-m.done }

// write writes the calls held; a model's write that fails puts its calls back for the next.
func (m *callMeter) write(ctx context.Context) {
	m.mu.Lock()
	batch, unkept := m.pending, m.unkept
	m.pending, m.unkept = map[int64][]store.CallRecord{}, 0
	m.mu.Unlock()
	if unkept > 0 {
		logger.Warning(m.log, nil, fmt.Sprintf("%s: %d calls not kept, the store not written for too long", m.what, unkept))
	}
	for id, recs := range batch {
		if err := m.record(ctx, id, recs); err == nil {
			continue
		} else {
			logger.Warning(m.log, err, m.what+" not recorded yet: kept for the next write")
		}
		m.mu.Lock()
		back := append(recs, m.pending[id]...)
		if over := len(back) - maxPending; over > 0 {
			back, m.unkept = back[over:], m.unkept+over
		}
		m.pending[id] = back
		m.mu.Unlock()
	}
}
