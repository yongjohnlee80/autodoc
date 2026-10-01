// Package daemon is what --serve runs over the store: the workspaces it holds, each served by an
// indexer and a follower of its own, and changed while the daemon runs by the workspace verbs.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/embed"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// Workspaces is the daemon's rpc.Workspaces: the store's workspaces, each served by an indexer
// and a follower of its own while it is open.
type Workspaces struct {
	db   *store.Store
	ctx  context.Context
	opts Options

	// chg serialises the changes — Add, Rename, Remove, SetEmbedding, OpenAll — each a sequence over
	// the store and the served set that must not interleave with another, and it guards
	// opts.Provider. mu guards only the map, and is never held across a stop or a start: a change
	// that takes seconds (a provider handed to every workspace) must not hold up List, Get, and with
	// them every search, listing and status, while it runs.
	chg    sync.Mutex
	mu     sync.Mutex
	served map[string]*served // by name

	// what index.status reports as warming up, under mu: a provider being set up for every
	// workspace, and the workspaces restarting to take one
	setup      string
	restarting map[string]bool
}

// The warming-up reasons index.status reports, beside a follower's first scan (its "starting").
const (
	warmRestarting = "restarting with the new embedding provider"
)

// SetWarming says a provider is being set up for every workspace ("" when none is): the probe
// that loads its model can take a while, and clients say so rather than look idle.
func (m *Workspaces) SetWarming(reason string) {
	m.mu.Lock()
	m.setup = reason
	m.mu.Unlock()
}

// warming is what workspace name is waiting on, for index.status: nothing once it is ready.
func (m *Workspaces) warming(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	if m.setup != "" {
		out = append(out, m.setup)
	}
	if m.restarting[name] {
		out = append(out, warmRestarting)
	}
	return out
}

// served is one workspace as it runs: what the API sees, and how to stop it.
type served struct {
	id   int64
	w    *rpc.Workspace
	halt func() // stops its indexer and follower; its root stays open; nil for one not opened
	stop func() // halts it and closes its root; nil for one not opened
}

// Options are how the daemon serves a workspace.
type Options struct {
	Poll     time.Duration // the follower's listing interval
	Provider embed.Provider
	Log      logger.Logger
	// BatchDelay is the indexer's (0: its default); tests shorten it.
	BatchDelay time.Duration
}

// New is the workspaces of db, served until ctx ends. Nothing is served until OpenAll.
func New(ctx context.Context, db *store.Store, o Options) *Workspaces {
	if o.Log == nil {
		o.Log = logger.New()
	}
	return &Workspaces{db: db, ctx: ctx, opts: o, served: map[string]*served{}, restarting: map[string]bool{}}
}

// pastDefaultExcludes are the exclude patterns a workspace was given by default before the
// current default: one stored with exactly one of them never chose its patterns, so it is moved to
// the current default when the daemon starts, as a workspace added today would have it. Patterns
// anyone changed are never touched.
var pastDefaultExcludes = [][]string{
	{".git/**"}, // before v0.1.4 added **/node_modules/**
}

// OpenAll serves every workspace the store has. One whose root cannot be opened is listed with its
// error, and served once it is added again. A workspace still on a past default exclude moves to
// the current one first.
func (m *Workspaces) OpenAll() error {
	m.chg.Lock()
	defer m.chg.Unlock()
	ws, err := m.db.Workspaces(m.ctx)
	if err != nil {
		return err
	}
	for i, w := range ws {
		if !pastDefault(w.Exclude) {
			continue
		}
		if err := m.db.SetWorkspaceExclude(m.ctx, w.ID, config.DefaultExclude); err != nil {
			logger.Warning(m.opts.Log, err, "workspace kept its old default exclude: "+w.Name)
			continue
		}
		ws[i].Exclude = config.DefaultExclude
		logger.Info(m.opts.Log, logger.Fields{"event": "workspace moved to the current default exclude",
			"workspace": w.Name, "exclude": config.DefaultExclude})
	}
	for _, w := range ws {
		s := m.serve(w.ID, config.Workspace{Name: w.Name, Root: w.Root, Include: w.Include, Exclude: w.Exclude})
		m.mu.Lock()
		m.served[w.Name] = s
		m.mu.Unlock()
	}
	return nil
}

func (m *Workspaces) warmingOf(name string) func() []string {
	return func() []string { return m.warming(name) }
}

func pastDefault(exclude []string) bool {
	for _, past := range pastDefaultExcludes {
		if slices.Equal(exclude, past) {
			return true
		}
	}
	return false
}

// serve starts a stored workspace, or lists it with the error that kept it from starting.
func (m *Workspaces) serve(id int64, c config.Workspace) *served {
	s, err := m.start(id, c)
	if err != nil {
		logger.Warning(m.opts.Log, err, "workspace not served: "+c.Name)
		s = &served{id: id, w: &rpc.Workspace{Name: c.Name, Root: c.Root, Include: c.Include, Exclude: c.Exclude, Err: err}}
	}
	return s
}

// start opens a workspace's root and starts its indexer and follower.
func (m *Workspaces) start(id int64, c config.Workspace) (*served, error) {
	if c.Include == nil {
		c.Include = config.DefaultInclude
	}
	if c.Exclude == nil {
		c.Exclude = config.DefaultExclude
	}
	ws, err := workspace.Open(c)
	if err != nil {
		return nil, err
	}
	ix := index.NewIndexer(index.Open(m.db, id), ws.FS, index.Options{Match: ws.Matcher.Match, Provider: m.opts.Provider,
		BatchDelay: m.opts.BatchDelay})
	f := follow.New(ws.FS, ix, ix, follow.Options{PollInterval: m.opts.Poll, Match: ws.Matcher.Match, Excluded: ws.Matcher.Excluded})
	ix.SetRescanner(f) // index.reindex(ws, "") finds the files the index lacks through the follower
	ctx, cancel := context.WithCancel(m.ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := ix.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error(m.opts.Log, err, "indexer stopped: "+c.Name)
		}
	}()
	go func() {
		defer wg.Done()
		_ = f.Run(ctx)
	}()
	halt := func() {
		cancel()
		wg.Wait()
	}
	stop := func() {
		halt()
		_ = ws.Close()
	}
	w := &rpc.Workspace{Name: c.Name, Root: c.Root, Include: c.Include, Exclude: c.Exclude,
		Index: ix, Docs: docs.New(ws.FS, ws.Matcher.Match), Following: f.Status, Warming: m.warmingOf(c.Name)}
	return &served{id: id, w: w, halt: halt, stop: stop}, nil
}

// restartHook, when set by this package's tests, runs in SetEmbedding after a workspace's indexer
// and follower stopped and before its replacement starts. Production code never sets it.
var restartHook func(name string)

// SetEmbedding gives every workspace the provider p (nil: none, search by words): each served one
// is stopped and started again with it, its index as it was. While one restarts, the API keeps
// answering from the one stopping — its store reads and its root stay usable — so searches,
// listings and status never wait for the restart; the old root closes once the new one is in.
func (m *Workspaces) SetEmbedding(p embed.Provider) {
	m.chg.Lock()
	defer m.chg.Unlock()
	m.opts.Provider = p
	m.mu.Lock()
	running := make(map[string]*served, len(m.served))
	for name, s := range m.served {
		running[name] = s
		if s.halt != nil {
			m.restarting[name] = true // until its replacement is in: index.status says so
		}
	}
	m.mu.Unlock()
	for name, s := range running {
		if s.halt == nil {
			continue // not served: its root is gone
		}
		s.halt()
		if restartHook != nil {
			restartHook(name)
		}
		w := s.w
		next := m.serve(s.id, config.Workspace{Name: w.Name, Root: w.Root, Include: w.Include, Exclude: w.Exclude})
		m.mu.Lock()
		m.served[name] = next
		delete(m.restarting, name)
		m.mu.Unlock()
		s.stop() // the halt is done; this closes the old root
	}
}

// Replacing is the model a switch is replacing: the active model of a served workspace whose
// target is another; "" when none is switching.
func (m *Workspaces) Replacing(ctx context.Context) string {
	m.mu.Lock()
	var ixs []*index.Indexer
	for _, s := range m.served {
		if s.stop != nil {
			ixs = append(ixs, s.w.Index)
		}
	}
	m.mu.Unlock()
	for _, ix := range ixs {
		if st, err := ix.Status(ctx); err == nil && st.Embeddings != nil && st.Embeddings.Target != "" {
			return st.Embeddings.Model
		}
	}
	return ""
}

// StopAll stops every workspace, for the daemon's shutdown.
func (m *Workspaces) StopAll() {
	m.mu.Lock()
	all := m.served
	m.served = map[string]*served{}
	m.mu.Unlock()
	for _, s := range all {
		if s.stop != nil {
			s.stop()
		}
	}
}

func (m *Workspaces) List() []*rpc.Workspace {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*rpc.Workspace, 0, len(m.served))
	for _, s := range m.served {
		out = append(out, s.w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Workspaces) Get(name string) (*rpc.Workspace, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.served[name]
	if !ok {
		return nil, false
	}
	return s.w, true
}

// Add checks the workspace, records it, and serves it. The root must be a directory; the name and
// the root must be the store's only ones.
func (m *Workspaces) Add(ctx context.Context, c config.Workspace) (*rpc.Workspace, error) {
	c, err := config.NormalizeWorkspace(c)
	if err != nil {
		return nil, err
	}
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	_, taken := m.served[c.Name]
	m.mu.Unlock()
	if taken {
		return nil, fmt.Errorf("%w: %s", store.ErrTaken, c.Name)
	}
	// the root is checked before anything is recorded: a workspace is never stored unservable
	if err := checkRoot(c); err != nil {
		return nil, err
	}
	row, err := m.db.AddWorkspace(ctx, c.Name, c.Root, c.Include, c.Exclude)
	if err != nil {
		return nil, err
	}
	s, err := m.start(row.ID, c)
	if err != nil {
		// the root went away between the check and the start: undo the row
		_ = m.db.RemoveWorkspace(context.WithoutCancel(ctx), row.ID)
		return nil, err
	}
	m.mu.Lock()
	m.served[c.Name] = s
	m.mu.Unlock()
	return s.w, nil
}

func checkRoot(c config.Workspace) error {
	ws, err := workspace.Open(c)
	if err != nil {
		return err
	}
	return ws.Close()
}

// Rename gives a workspace a new name: one row. Its indexer keeps running.
func (m *Workspaces) Rename(ctx context.Context, name, to string) error {
	if _, err := config.NormalizeWorkspace(config.Workspace{Name: to, Root: "/"}); err != nil {
		return err
	}
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.served[name]
	if !ok {
		return fmt.Errorf("%w: %s", store.ErrNoWorkspace, name)
	}
	if name == to {
		return nil
	}
	if _, taken := m.served[to]; taken {
		return fmt.Errorf("%w: %s", store.ErrTaken, to)
	}
	if err := m.db.RenameWorkspace(ctx, s.id, to); err != nil {
		return err
	}
	w := *s.w
	w.Name = to
	w.Warming = m.warmingOf(to)
	s.w = &w
	delete(m.served, name)
	m.served[to] = s
	return nil
}

// Remove stops serving a workspace, then deletes it and, by the schema's cascade, its index, in
// one transaction. Its files are not touched. A delete that fails (the request cancelled, the
// store's write refused) leaves the workspace as it was: stored, and served again.
func (m *Workspaces) Remove(ctx context.Context, name string) error {
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	s, ok := m.served[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", store.ErrNoWorkspace, name)
	}
	if s.stop != nil {
		s.stop() // nothing writes its rows while they go
	}
	err := m.db.RemoveWorkspace(ctx, s.id)
	var back *served
	if err != nil && !errors.Is(err, store.ErrNoWorkspace) {
		w := s.w
		back = m.serve(s.id, config.Workspace{Name: w.Name, Root: w.Root, Include: w.Include, Exclude: w.Exclude})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if back != nil {
		m.served[name] = back
	} else { // the store no longer has it
		delete(m.served, name)
	}
	return err
}

var _ rpc.Workspaces = (*Workspaces)(nil)
