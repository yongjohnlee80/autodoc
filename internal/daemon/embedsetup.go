package daemon

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/core/hostprobe"
	"github.com/yongjohnlee80/autodoc/rpc"
	"github.com/yongjohnlee80/golib/search/embed"
)

// THE EMBEDDING SETUP (ADR 1793005098 §4.3): what the machine offers, a model pulled into Ollama
// as a job the daemon owns, and a model timed on a workspace's own texts.

// Probe is what this machine offers an embedding model, the Ollama at base asked for its version.
func (e *Embedding) Probe(ctx context.Context, base string) hostprobe.Host {
	return hostprobe.Probe(ctx, e.dataDir, base, e.client)
}

// Installed is model's identity on the Ollama at base when it is installed there: its digest and,
// from one probe embedding, its dims, so its fingerprint is the one the index would keep. Not
// installed is ok false, with no error, read from the server's list without loading anything. The
// probe loads the model, at tokens of context as the setup would use it, in a shared slot.
func (e *Embedding) Installed(ctx context.Context, base, model string, tokens int) (embed.Model, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	names, err := embed.Models(ctx, "ollama", base, "", e.client)
	if err != nil {
		return embed.Model{}, false, err
	}
	if !slices.Contains(names, model) && !slices.Contains(names, model+":latest") {
		return embed.Model{}, false, nil
	}
	var o *embed.Ollama
	err = e.ws.admitted(ctx, func() (err error) {
		o, err = embed.NewOllama(ctx, base, "", model, e.client, embed.WithContext(tokens))
		return err
	})
	if errors.Is(err, embed.ErrNoModel) {
		return embed.Model{}, false, nil
	}
	if err != nil {
		return embed.Model{}, false, err
	}
	return o.Model(), true, nil
}

// Sample embeds texts with model at base, at tokens of context, through the daemon's shared
// embedding slots (so it never jumps the queue the workspaces wait in), and answers the time a text
// took. ctx bounds it.
func (e *Embedding) Sample(ctx context.Context, base, model string, tokens int, texts []string) (time.Duration, error) {
	if len(texts) == 0 {
		return 0, nil
	}
	var per time.Duration
	err := e.ws.admitted(ctx, func() error {
		// one slot for both: the client's construction embeds a probe, which loads the model
		o, err := embed.NewOllama(ctx, base, "", model, e.client, embed.WithContext(tokens))
		if err != nil {
			return err
		}
		start := time.Now()
		if _, err := o.Embed(ctx, texts); err != nil {
			return err
		}
		per = time.Since(start) / time.Duration(len(texts))
		return nil
	})
	return per, err
}

// The states of a pull (rpc.PullState.State).
const (
	PullRunning   = "running"
	PullDone      = "done"
	PullFailed    = "failed"
	PullCancelled = "cancelled"
)

// rateWindow is how far back a pull's rate is measured.
const rateWindow = 10 * time.Second

// cancelWait bounds how long a cancel waits for its job to end before answering its state.
const cancelWait = 5 * time.Second

// pullJob is one pull, owned by the Embedding from its start to its end, never by the request that
// started it.
type pullJob struct {
	id, model string
	cancel    context.CancelFunc
	done      chan struct{} // closed when its goroutine ends

	mu        sync.Mutex
	state     string
	completed int64
	total     int64
	samples   []rateSample // the progress of the last rateWindow
	err       error
}

type rateSample struct {
	at        time.Time
	completed int64
}

// Pull starts pulling model into the Ollama at base, and answers the job's id at once. The job's
// context is the daemon's (Start's): the request ending never cancels it, and the daemon's
// shutdown does, Wait joining it. One pull at a time: another while one runs or unwinds is
// rpc.ErrPulling. An ended one is kept, for its status, until the next starts.
func (e *Embedding) Pull(base, model string) (string, error) {
	e.pullMu.Lock()
	defer e.pullMu.Unlock()
	if e.started == nil {
		return "", errors.New("daemon: the embedding has not started")
	}
	if j := e.job; j != nil {
		select {
		case <-j.done:
		default:
			return "", rpc.ErrPulling
		}
	}
	e.pulls++
	// the daemon's start, in nanoseconds, then a count: never reused, across fast restarts too
	id := "pull-" + strconv.FormatInt(e.born.UnixNano(), 36) + "-" + strconv.Itoa(e.pulls)
	ctx, cancel := context.WithCancel(e.started)
	j := &pullJob{id: id, model: model, cancel: cancel, done: make(chan struct{}), state: PullRunning}
	e.job = j
	e.pullsRunning.Add(1)
	go func() {
		defer e.pullsRunning.Done()
		defer close(j.done)
		defer cancel()
		err := embed.Pull(ctx, base, "", model, e.client, func(p embed.PullProgress) { j.progress(p, time.Now()) })
		j.mu.Lock()
		defer j.mu.Unlock()
		switch {
		case err == nil:
			j.state = PullDone
		case ctx.Err() != nil:
			j.state = PullCancelled
		default:
			j.state, j.err = PullFailed, err
		}
	}()
	return id, nil
}

// progress records a line of the pull's stream.
func (j *pullJob) progress(p embed.PullProgress, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.completed, j.total = p.Completed, p.Total
	j.samples = append(j.samples, rateSample{now, p.Completed})
	for len(j.samples) > 1 && now.Sub(j.samples[0].at) > rateWindow {
		j.samples = j.samples[1:]
	}
}

// stateOf is j as its status answers it.
func (j *pullJob) stateOf() rpc.PullState {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := rpc.PullState{Job: j.id, Model: j.model, State: j.state, Completed: j.completed, Total: j.total}
	if n := len(j.samples); n > 1 {
		a, b := j.samples[0], j.samples[n-1]
		if dt := b.at.Sub(a.at).Seconds(); dt > 0 && b.completed > a.completed {
			s.Rate = float64(b.completed-a.completed) / dt
		}
	}
	if s.Rate > 0 && s.Total > s.Completed && j.state == PullRunning {
		eta := float64(s.Total-s.Completed) / s.Rate
		s.ETA = &eta
	}
	if j.err != nil {
		s.Error = j.err.Error()
	}
	return s
}

// pullNamed is the job named id, if it is the current one; any other id is rpc.ErrNoPull, so a call
// naming an old job never reads or cancels a newer caller's.
func (e *Embedding) pullNamed(id string) (*pullJob, error) {
	e.pullMu.Lock()
	defer e.pullMu.Unlock()
	if e.job == nil || e.job.id != id {
		return nil, rpc.ErrNoPull
	}
	return e.job, nil
}

// PullStatus is job's state.
func (e *Embedding) PullStatus(job string) (rpc.PullState, error) {
	j, err := e.pullNamed(job)
	if err != nil {
		return rpc.PullState{}, err
	}
	return j.stateOf(), nil
}

// CancelPull cancels job if it still runs, waits (cancelWait at most) for it to end, and answers
// its state; an ended job answers its state unchanged.
func (e *Embedding) CancelPull(job string) (rpc.PullState, error) {
	j, err := e.pullNamed(job)
	if err != nil {
		return rpc.PullState{}, err
	}
	j.cancel()
	// NOTE: the job's end is the event; the timer only bounds how long the caller waits for it, so
	// a stream whose read is slow to see the cancel still gets an answer (its state as it stands).
	t := time.NewTimer(cancelWait)
	defer t.Stop()
	select {
	case <-j.done:
	case <-t.C:
	}
	return j.stateOf(), nil
}
