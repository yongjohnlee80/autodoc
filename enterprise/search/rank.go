package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/errs"
	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// A SEARCH'S SECOND STAGE: a ranker reorders recall's best candidates (golib's search/rank), from
// the texts the search itself read (docs/decisions/server.md#a-reranker-after-recall).
//
// A search with a ranker runs golib's stage over its engine: the engine is asked for the larger of
// the window and the limit, the window's top hits are ranked, the rest follow in recall order, and
// the answer is cut to the limit. Each candidate's text is its chunk's breadcrumb and body, kept by
// the View the search read them in (pgstore.View.KeepTexts), so the texts are the snapshot's and
// the scope's, and nothing is read again. A ranker that fails never fails the search: it answers in
// recall order, its Ranking saying why.

// Ranking is what a search's ranker did: golib's state (off, ready or error), the model, and on
// error the code of why (the Rank* codes).
type Ranking struct {
	State gsearch.RankState
	Model string
	Error string
}

// The codes of a Ranking in error: why a search answered in recall order.
const (
	RankUnreachable = "provider_unreachable" // the ranker could not be reached, or is down
	RankRefused     = "provider_refused"     // it refused the key or the model
	RankLimited     = "rate_limited"         // it said too many requests
	RankBadAnswer   = "bad_answer"           // it answered, but not a score for each text
	RankTimeout     = "timeout"              // its call ran past the search's ranker budget
	RankBusy        = "busy"                 // no ranker call could start within the budget
	RankInternal    = "internal"             // a candidate had no text: a defect
)

// ErrWindow is a request's window outside golib's bounds (rank.MinWindow to rank.MaxWindow).
var ErrWindow = errs.Sentinel(errs.ErrInvalidArgument, "search: the window is out of range")

// Coded is a ranker's error that names its own code (one of the Rank* codes): a server's ranker
// that is down, or busy, says so with it.
type Coded interface {
	error
	RankCode() string
}

// stage is a Searcher's or a Global's ranker: the ranker, its window, and the budget of one
// search's call of it.
type stage struct {
	r       rank.Ranker
	window  int
	timeout time.Duration
}

// newStage is r as a stage, the window held to golib's bounds; nil for a nil r.
func newStage(r rank.Ranker, window int, timeout time.Duration) *stage {
	if r == nil {
		return nil
	}
	return &stage{r: r, window: rank.Window(window), timeout: timeout}
}

// WithRanker adds the second stage: r reranks each search's top window candidates (rank.Window
// bounds it, 0 is rank.DefaultWindow), each search's call of it at most timeout (0: no budget of
// its own). A request may skip it (Request.NoRerank) or ask for another window (Request.Window).
// r is shared by every search; one that bounds its calls in flight answers a Coded RankBusy error
// for a call it cannot start under the context it is given.
func WithRanker(r rank.Ranker, window int, timeout time.Duration) Option {
	return func(o *options) { o.rank = newStage(r, window, timeout) }
}

// checkWindow refuses a request's window outside golib's bounds; 0 is the stage's own.
func checkWindow(n int) error {
	if n != 0 && (n < rank.MinWindow || n > rank.MaxWindow) {
		return fmt.Errorf("%w: %d (%d to %d)", ErrWindow, n, rank.MinWindow, rank.MaxWindow)
	}
	return nil
}

// outcome is one search's ranker call as the stage's wrappers saw it: the code of the first
// failure. golib's Ranking carries a constant message, so the code is the server's own.
type outcome struct {
	mu   sync.Mutex
	code string
}

func (o *outcome) fail(code string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.code == "" {
		o.code = code
	}
}

func (o *outcome) failed() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.code
}

// ranking is golib's Ranking as the search answers it: an error's code from o, or bad_answer when
// none was recorded (golib's own check found the scores' count wrong).
func (o *outcome) ranking(r gsearch.Ranking) Ranking {
	out := Ranking{State: r.State, Model: r.Model}
	if r.State == "" {
		out.State = gsearch.RankOff
	}
	if r.State == gsearch.RankError {
		out.Error = o.failed()
		if out.Error == "" {
			out.Error = RankBadAnswer
		}
	}
	return out
}

// timed is a stage's ranker for one search: each call under the stage's budget, a failure's code
// recorded on the search's outcome. The budget is the ranker's alone: golib reads the search's own
// context ending as the search's error, so the budget ends the call and not the search.
type timed struct {
	st  *stage
	out *outcome
}

func (t timed) Model() rank.Model { return t.st.r.Model() }

func (t timed) Rank(ctx context.Context, query string, texts []string) ([]float64, error) {
	if t.st.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.st.timeout)
		defer cancel()
	}
	scores, err := t.st.r.Rank(ctx, query, texts)
	if err != nil {
		t.out.fail(RankCode(ctx, err))
	}
	return scores, err
}

// RankCode is why a ranker's call under ctx failed, as a Rank* code: a Coded error's own, a
// deadline's timeout, or golib's sentinel's.
func RankCode(ctx context.Context, err error) string {
	var c Coded
	switch {
	case errors.As(err, &c):
		return c.RankCode()
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return RankTimeout
	case errors.Is(err, rank.ErrUnauthorized), errors.Is(err, rank.ErrNoModel), errors.Is(err, rank.ErrNotARanker),
		errors.Is(err, rank.ErrRefused):
		return RankRefused
	case errors.Is(err, rank.ErrRateLimited):
		return RankLimited
	case errors.Is(err, rank.ErrBadAnswer):
		return RankBadAnswer
	case errors.Is(err, rank.ErrUnreachable):
		return RankUnreachable
	}
	return RankUnreachable
}

// source is a stage's ranker and one search's window: golib's Source for one search.
type source struct {
	r      rank.Ranker
	window int
}

func (s source) Current() (rank.Ranker, int) { return s.r, s.window }

// texts is the TextSource of one search's call: each hit's text as its View kept it, by path and
// byte range. A hit with none is RankInternal: every candidate of a View that keeps texts has one.
func texts(c *call, out *outcome) rank.TextSource {
	return func(_ context.Context, hits []gsearch.Hit) ([]string, error) {
		return textsOf(c.texts, hits, out)
	}
}

func textsOf(kept map[pgstore.TextKey]string, hits []gsearch.Hit, out *outcome) ([]string, error) {
	ts := make([]string, len(hits))
	for i, h := range hits {
		t, ok := kept[pgstore.TextKey{Path: h.Path, Start: h.ByteStart, End: h.ByteEnd}]
		if !ok {
			out.fail(RankInternal)
			return nil, fmt.Errorf("search: the hit %s has no text kept by its view", h.Path)
		}
		ts[i] = t
	}
	return ts, nil
}

// ranked is one search through the stage: q's hits reranked by the stage's ranker over the
// window, the texts c's View kept.
func (s *Searcher) ranked(ctx context.Context, eng gsearch.Searcher, q gsearch.Query, window int, c *call) (gsearch.Result, Ranking, error) {
	if window == 0 {
		window = s.rank.window
	}
	out := &outcome{}
	rs := rank.NewSearcher(eng, texts(c, out), source{r: timed{st: s.rank, out: out}, window: window},
		rank.WithDefaultLimit(s.def), rank.WithMaxLimit(s.max))
	res, err := rs.Search(ctx, q)
	if err != nil {
		return res, Ranking{}, err
	}
	return res, out.ranking(res.Rank), nil
}
