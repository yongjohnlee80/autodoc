package daemon

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"
)

// The daemon owns the provider's capacity, not any one workspace. An unfinished
// focused workspace gets sixteen batches before one background turn; thus it
// gets at least 16/17 of the device while an endlessly edited focused file
// cannot starve the remaining workspaces.
const (
	focusTTL   = 15 * time.Second
	searchTTL  = 3 * time.Minute
	queuePoll  = 5 * time.Second
	focusBurst = 16
)

type embeddingQueue struct {
	host      *Workspaces
	mu        sync.Mutex
	wake      chan struct{}
	cancel    context.CancelFunc
	done      chan struct{}
	focus     string
	focusedAt time.Time
	searched  map[string]time.Time
	idle      map[string]time.Time
	policy    map[string]string
	activated map[string]bool
	running   map[string]bool
	last      string // round-robin cursor, by workspace name
	streak    int
	// lastProvider is the provider of the last background turn ("" the daemon's), and sameRun how
	// many background turns in a row it had: the next prefers a workspace on the same provider, so
	// a server holding one model is not made to swap models every batch, up to focusBurst turns
	lastProvider string
	sameRun      int
}

func newEmbeddingQueue(host *Workspaces) *embeddingQueue {
	return &embeddingQueue{host: host, wake: make(chan struct{}, 1), searched: map[string]time.Time{}, idle: map[string]time.Time{},
		policy: map[string]string{}, activated: map[string]bool{}, running: map[string]bool{}}
}

func (q *embeddingQueue) setPolicy(name, policy string) bool {
	q.mu.Lock()
	q.policy[name] = policy
	if policy == "when opened" && q.focus == name && time.Since(q.focusedAt) <= focusTTL {
		q.activated[name] = true
	}
	active := policy == "always" || (policy == "when opened" && q.activated[name])
	delete(q.idle, name)
	q.mu.Unlock()
	q.signal()
	return active
}

func (q *embeddingQueue) rename(from, to string) {
	q.mu.Lock()
	q.policy[to], q.activated[to], q.searched[to], q.idle[to] = q.policy[from], q.activated[from], q.searched[from], q.idle[from]
	delete(q.policy, from)
	delete(q.activated, from)
	delete(q.searched, from)
	delete(q.idle, from)
	if q.focus == from {
		q.focus = to
	}
	q.mu.Unlock()
	q.signal()
}

func (q *embeddingQueue) forget(name string) {
	q.mu.Lock()
	delete(q.policy, name)
	delete(q.activated, name)
	delete(q.searched, name)
	delete(q.idle, name)
	if q.focus == name {
		q.focus = ""
	}
	q.mu.Unlock()
}

func (q *embeddingQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *embeddingQueue) wakeWorkspace(name string) {
	q.mu.Lock()
	delete(q.idle, name)
	q.mu.Unlock()
	q.signal()
}

func (q *embeddingQueue) start() {
	q.mu.Lock()
	if q.done != nil || q.host.ctx.Err() != nil {
		q.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(q.host.ctx)
	done := make(chan struct{})
	q.cancel, q.done = cancel, done
	q.mu.Unlock()
	go func() {
		defer close(done)
		var workers sync.WaitGroup
		for range q.host.opts.MaxEmbedRequests {
			workers.Add(1)
			go func() { defer workers.Done(); q.run(ctx) }()
		}
		workers.Wait()
	}()
	q.signal()
}

func (q *embeddingQueue) stop() {
	q.mu.Lock()
	cancel, done := q.cancel, q.done
	q.cancel, q.done = nil, nil
	q.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (q *embeddingQueue) focusWorkspace(name string) bool {
	q.mu.Lock()
	q.focus, q.focusedAt = name, time.Now()
	q.activated[name] = true
	delete(q.idle, name)
	active := q.policy[name] != "never"
	q.mu.Unlock()
	q.signal()
	return active
}

func (q *embeddingQueue) searchWorkspace(name string) {
	q.mu.Lock()
	q.searched[name] = time.Now()
	delete(q.idle, name)
	q.mu.Unlock()
	q.signal()
}

// queueState describes a workspace relative to the single current batch.
func (q *embeddingQueue) queueState(name string) (state, behind string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.policy[name] == "never" || (q.policy[name] == "when opened" && !q.activated[name]) {
		return "paused", ""
	}
	if q.running[name] {
		return "running", ""
	}
	if q.running[q.focus] {
		return "waiting", q.focus
	}
	for active := range q.running {
		return "waiting", active
	}
	return "waiting", ""
}

func (q *embeddingQueue) run(ctx context.Context) {
	tick := time.NewTicker(queuePoll)
	defer tick.Stop()
	for ctx.Err() == nil {
		q.host.mu.Lock()
		candidates := make(map[string]*served, len(q.host.served))
		for name, s := range q.host.served {
			if s.ctx != nil && s.w.Index != nil && s.w.Index.HasEmbeddings() && s.w.Index.EmbeddingReady() {
				candidates[name] = s
			}
		}
		q.host.mu.Unlock()
		name, position := q.choose(candidates)
		if name == "" {
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
			case <-tick.C:
			}
			continue
		}
		s := candidates[name]
		batchCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(s.ctx, cancel)
		worked, err := s.w.Index.EmbedBatch(batchCtx, position)
		stop()
		cancel()
		q.mu.Lock()
		delete(q.running, name)
		if err != nil || !worked {
			q.idle[name] = time.Now().Add(queuePoll)
		}
		q.mu.Unlock()
		if err != nil && ctx.Err() == nil && s.ctx.Err() == nil {
			logger.Warning(q.host.opts.Log, err, logger.Fields{"event": "embedding.queue.batch.failed", "workspace": name})
		}
		q.signal()
	}
}

func (q *embeddingQueue) choose(candidates map[string]*served) (string, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	var names []string
	for name := range candidates {
		if q.running[name] {
			continue
		}
		if q.policy[name] == "never" || (q.policy[name] == "when opened" && !q.activated[name]) {
			continue
		}
		if !now.Before(q.idle[name]) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", 0
	}
	sort.Strings(names)
	focused := q.focus
	if now.Sub(q.focusedAt) > focusTTL {
		focused = ""
	}
	focusedAvailable := focused != "" && candidates[focused] != nil && !q.running[focused] && !now.Before(q.idle[focused])
	other := make([]string, 0, len(names))
	for _, name := range names {
		if name != focused {
			other = append(other, name)
		}
	}
	roundRobin := func(list []string) string {
		for _, name := range list {
			if name > q.last {
				return name
			}
		}
		return list[0]
	}
	// a background turn stays on the last turn's provider while one of its workspaces has work,
	// for at most focusBurst turns in a row, then moves on: affinity without starvation
	chooseNext := func(list []string) string {
		if q.sameRun < focusBurst {
			var same []string
			for _, n := range list {
				if candidates[n].override == q.lastProvider {
					same = append(same, n)
				}
			}
			if len(same) > 0 {
				return roundRobin(same)
			}
		}
		var others []string
		for _, n := range list {
			if candidates[n].override != q.lastProvider {
				others = append(others, n)
			}
		}
		if len(others) > 0 {
			return roundRobin(others)
		}
		return roundRobin(list)
	}
	name := ""
	if focusedAvailable && (q.streak < focusBurst || len(other) == 0) {
		name = focused
		q.streak++
	} else if len(other) > 0 {
		// A periodic fair turn sees all other workspaces, including those never searched.
		if focused != "" && q.streak >= focusBurst {
			name = chooseNext(other)
		} else {
			var recent []string
			for _, n := range other {
				if now.Sub(q.searched[n]) < searchTTL {
					recent = append(recent, n)
				}
			}
			if len(recent) > 0 {
				name = chooseNext(recent)
			} else {
				name = chooseNext(other)
			}
		}
		q.last, q.streak = name, 0
		if p := candidates[name].override; p == q.lastProvider {
			q.sameRun++
		} else {
			q.lastProvider, q.sameRun = p, 1
		}
	} else if focusedAvailable {
		name = focused
	}
	if name != "" {
		q.running[name] = true
	}
	position := 1
	if focused != "" && name != focused {
		if q.running[focused] {
			position = 2
		}
		for _, n := range names {
			if n == focused {
				position = 2
				break
			}
		}
	}
	return name, position
}
