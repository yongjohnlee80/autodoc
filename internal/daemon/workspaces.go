// Package daemon is what --serve runs over the store: the workspaces it holds, each served by an
// indexer and a follower of its own, and changed while the daemon runs by the workspace verbs.
package daemon

import (
	"context"
	"errors"
	"fmt"
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

	mu     sync.Mutex
	served map[string]*served // by name
}

// served is one workspace as it runs: what the API sees, and how to stop it.
type served struct {
	id   int64
	w    *rpc.Workspace
	stop func() // stops its indexer and follower, and closes its root; nil for one not opened
}

// Options are how the daemon serves a workspace.
type Options struct {
	Poll        time.Duration // the follower's listing interval
	Provider    embed.Provider
	ProviderFor func(embed.Model) (embed.Provider, error)
	Log         logger.Logger
	// BatchDelay is the indexer's (0: its default); tests shorten it.
	BatchDelay time.Duration
}

// New is the workspaces of db, served until ctx ends. Nothing is served until OpenAll.
func New(ctx context.Context, db *store.Store, o Options) *Workspaces {
	if o.Log == nil {
		o.Log = logger.New()
	}
	return &Workspaces{db: db, ctx: ctx, opts: o, served: map[string]*served{}}
}

// OpenAll serves every workspace the store has. One whose root cannot be opened is listed with its
// error, and served once it is added again.
func (m *Workspaces) OpenAll() error {
	ws, err := m.db.Workspaces(m.ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		s := m.serve(w.ID, config.Workspace{Name: w.Name, Root: w.Root, Include: w.Include, Exclude: w.Exclude})
		m.mu.Lock()
		m.served[w.Name] = s
		m.mu.Unlock()
	}
	return nil
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
		ProviderFor: m.opts.ProviderFor, BatchDelay: m.opts.BatchDelay})
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
	stop := func() {
		cancel()
		wg.Wait()
		_ = ws.Close()
	}
	w := &rpc.Workspace{Name: c.Name, Root: c.Root, Include: c.Include, Exclude: c.Exclude,
		Index: ix, Docs: docs.New(ws.FS, ws.Matcher.Match), Following: f.Status}
	return &served{id: id, w: w, stop: stop}, nil
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, taken := m.served[c.Name]; taken {
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
	m.served[c.Name] = s
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
	s.w = &w
	delete(m.served, name)
	m.served[to] = s
	return nil
}

// Remove stops serving a workspace, then deletes it and, by the schema's cascade, its index, in
// one transaction. Its files are not touched. A delete that fails (the request cancelled, the
// store's write refused) leaves the workspace as it was: stored, and served again.
func (m *Workspaces) Remove(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.served[name]
	if !ok {
		return fmt.Errorf("%w: %s", store.ErrNoWorkspace, name)
	}
	if s.stop != nil {
		s.stop() // nothing writes its rows while they go
	}
	err := m.db.RemoveWorkspace(ctx, s.id)
	switch {
	case err == nil, errors.Is(err, store.ErrNoWorkspace): // the store no longer has it
		delete(m.served, name)
	default:
		w := s.w
		m.served[name] = m.serve(s.id, config.Workspace{Name: w.Name, Root: w.Root, Include: w.Include, Exclude: w.Exclude})
	}
	return err
}

var _ rpc.Workspaces = (*Workspaces)(nil)
