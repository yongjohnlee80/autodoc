package embedder_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/search/embed"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// provider is an embedding provider with no network: each text's vector is made from its hash,
// three times longer than a unit vector so normalizing shows. It counts every text it is asked
// for, can fail its next calls, rejects the texts named in reject (once each, unless always), and
// can hold every call until released.
type provider struct {
	name string
	dims int

	mu       sync.Mutex
	asked    map[string]int // texts, by how many times a call carried them
	calls    int
	fail     int             // the next calls that fail as a whole
	reject   map[string]bool // texts rejected, as ErrRejected
	always   bool            // reject keeps rejecting, rather than once
	inFlight int
	most     int           // the most calls in flight at once
	hold     chan struct{} // when set, every call waits for it to close
	arrived  chan struct{} // when set, signalled as each call arrives
}

func newProvider(name string) *provider {
	return &provider{name: name, dims: 4, asked: map[string]int{}, reject: map[string]bool{}}
}

func (p *provider) Name() string { return "fake" }
func (p *provider) Model() embed.Model {
	return embed.Model{Provider: "fake", Name: p.name, Dims: p.dims}
}

func (p *provider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	p.mu.Lock()
	p.calls++
	p.inFlight++
	p.most = max(p.most, p.inFlight)
	hold, arrived := p.hold, p.arrived
	failing := p.fail > 0
	if failing {
		p.fail--
	}
	var rejected bool
	for _, t := range texts {
		if p.reject[t] {
			rejected = true
		}
	}
	if rejected && len(texts) == 1 && !p.always {
		delete(p.reject, texts[0])
	}
	if !failing && !rejected {
		for _, t := range texts {
			p.asked[t]++
		}
	}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
	}()
	if arrived != nil {
		select {
		case arrived <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	switch {
	case failing:
		return nil, fmt.Errorf("%w: the fake provider is down", embed.ErrUnreachable)
	case rejected:
		return nil, fmt.Errorf("%w: the fake provider rejects one of %d texts", embed.ErrRejected, len(texts))
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = vec(t, p.dims)
	}
	return out, nil
}

// times is how many times the provider embedded text.
func (p *provider) times(text string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.asked[text]
}

func (p *provider) texts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.asked)
}

func (p *provider) mostInFlight() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.most
}

func (p *provider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// vec is text's test vector: its hash's first dims bytes, each in 1..256, times three.
func vec(text string, dims int) []float32 {
	h := sha256.Sum256([]byte(text))
	out := make([]float32, dims)
	for i := range out {
		out[i] = 3 * float32(int(h[i])+1)
	}
	return out
}

// built is a Build answering p.
func built(p *provider) func(context.Context) (embed.Provider, error) {
	return func(context.Context) (embed.Provider, error) { return p, nil }
}

// store is an in-memory tenant index with pgstore's semantics, as far as the worker uses them:
// a document is ready when every one of its chunk texts has a vector under the model it is asked
// about, and a write of it makes it unready at a new generation.
type store struct {
	mu      sync.Mutex
	docs    map[string]*doc
	vecs    map[string]map[[32]byte][]float32 // model → hash → vector
	active  string
	target  string
	writes  int // every write the worker made
	pending []map[[32]byte]bool
}

type doc struct {
	gen   int64
	texts []string // embed texts, in order
	ready bool
}

func newStore() *store {
	return &store{docs: map[string]*doc{}, vecs: map[string]map[[32]byte][]float32{}}
}

func hashOf(text string) [32]byte { return sha256.Sum256([]byte(text)) }

// put writes a document of texts, unready at its next generation.
func (s *store) put(path string, texts ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[path]
	if d == nil {
		d = &doc{}
		s.docs[path] = d
	}
	d.gen++
	d.texts, d.ready = texts, false
}

func (s *store) isReady(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[path] != nil && s.docs[path].ready
}

func (s *store) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func (s *store) has(model, text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.vecs[model][hashOf(text)]
	return ok
}

// lastSkip is the skip set Pending was last given.
func (s *store) lastSkip() map[[32]byte]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	return s.pending[len(s.pending)-1]
}

func (s *store) paths() []string {
	out := make([]string, 0, len(s.docs))
	for p := range s.docs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (s *store) Models(context.Context) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active, s.target, nil
}

func (s *store) SetModels(_ context.Context, active, target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	s.active, s.target = active, target
	return nil
}

func (s *store) ReclaimModels(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for m, v := range s.vecs {
		if m != s.active && m != s.target {
			n += len(v)
			delete(s.vecs, m)
		}
	}
	return n, nil
}

func (s *store) Pending(_ context.Context, model string, skip map[[32]byte]bool, limit int) ([]pgstore.Text, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, skip)
	seen := map[[32]byte]bool{}
	var out []pgstore.Text
	for _, p := range s.paths() {
		for _, t := range s.docs[p].texts {
			h := hashOf(t)
			if _, ok := s.vecs[model][h]; ok || seen[h] || skip[h] || len(out) == limit {
				continue
			}
			seen[h] = true
			out = append(out, pgstore.Text{TextHash: h, Text: t})
		}
	}
	return out, nil
}

func (s *store) Embed(_ context.Context, model string, vecs []pgstore.Embedding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.vecs[model] == nil {
		s.vecs[model] = map[[32]byte][]float32{}
	}
	for _, e := range vecs {
		if _, ok := s.vecs[model][e.TextHash]; !ok {
			s.vecs[model][e.TextHash] = e.Vec
		}
	}
	return nil
}

func (s *store) MarkReady(_ context.Context, path, model string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[path]
	if d == nil {
		return false, errors.New("no such document")
	}
	s.writes++
	d.ready = true
	for _, t := range d.texts {
		if _, ok := s.vecs[model][hashOf(t)]; !ok {
			d.ready = false
		}
	}
	return d.ready, nil
}

func (s *store) page(after string, limit int, unready bool) ([]pgstore.DocInfo, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []pgstore.DocInfo
	for _, p := range s.paths() {
		if p <= after || (unready && s.docs[p].ready) {
			continue
		}
		if len(out) == limit {
			return out, true, nil
		}
		out = append(out, pgstore.DocInfo{Path: p, Generation: s.docs[p].gen})
	}
	return out, false, nil
}

func (s *store) Docs(_ context.Context, after string, limit int) ([]pgstore.DocInfo, bool, error) {
	return s.page(after, limit, false)
}

func (s *store) Unready(_ context.Context, after string, limit int) ([]pgstore.DocInfo, bool, error) {
	return s.page(after, limit, true)
}

func (s *store) DocsWithTexts(_ context.Context, hashes [][32]byte) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, p := range s.paths() {
		for _, t := range s.docs[p].texts {
			if slices.Contains(hashes, hashOf(t)) {
				out = append(out, p)
				break
			}
		}
	}
	return out, nil
}

// waitFor polls cond for up to five seconds.
func waitFor(t interface {
	Helper()
	Fatalf(string, ...any)
}, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 5 s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fingerprint is a fake provider's model as the store records it.
func fingerprint(name string) string { return "fake|" + name + "||4" }
