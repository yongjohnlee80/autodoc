// Package daemon is what --serve runs over the store: the workspaces it holds, each served by an
// indexer and a follower of its own, and changed while the daemon runs by the workspace verbs.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/embed"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/schema"
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
	queue      *embeddingQueue
	// schemas are the workspaces' frontmatter schemas, by workspace id, under mu: each outlives its
	// workspace's restarts, so the last valid schema survives a pattern or provider change.
	schemas map[int64]*schemaHolder
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
	ctx  context.Context
	halt func() // stops its indexer and follower; its root stays open; nil for one not opened
	stop func() // halts it and closes its root; nil for one not opened
}

// Options are how the daemon serves a workspace.
type Options struct {
	Poll             time.Duration // the follower's listing interval
	Provider         embed.Provider
	Log              logger.Logger
	MaxEmbedRequests int // across the daemon, 1 by default, 2 maximum
	// BatchDelay is the indexer's (0: its default); tests shorten it.
	BatchDelay time.Duration
}

// New is the workspaces of db, served until ctx ends. Nothing is served until OpenAll.
func New(ctx context.Context, db *store.Store, o Options) *Workspaces {
	if o.MaxEmbedRequests <= 0 {
		o.MaxEmbedRequests = 1
	}
	o.MaxEmbedRequests = min(o.MaxEmbedRequests, 2)
	o.Provider = withProviderSlots(o.Provider, o.MaxEmbedRequests)
	if o.Log == nil {
		o.Log = logger.New()
	}
	m := &Workspaces{db: db, ctx: ctx, opts: o, served: map[string]*served{}, restarting: map[string]bool{},
		schemas: map[int64]*schemaHolder{}}
	m.queue = newEmbeddingQueue(m)
	return m
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
	m.queue.start()
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
	sectionTokens, err := m.db.SectionTokens(m.ctx, id)
	if err != nil {
		return nil, err
	}
	policy, err := m.db.EmbeddingPolicy(m.ctx, id)
	if err != nil {
		return nil, err
	}
	if c.Include == nil {
		c.Include = config.DefaultInclude
	}
	if c.Exclude == nil {
		c.Exclude = config.DefaultExclude
	}
	schemaPath, err := m.db.SchemaPath(m.ctx, id)
	if err != nil {
		return nil, err
	}
	ws, err := workspace.Open(c)
	if err != nil {
		return nil, err
	}
	// the schema is read before the indexer runs, so its first pass compares the right fingerprint
	sh := m.schemaHolder(id, c.Root)
	sh.setPath(schemaPath)
	sh.watch(m.ctx, schemaPoll)
	ix := index.NewIndexer(index.Open(m.db, id), ws.FS, index.Options{Match: ws.Matcher.Match, Provider: m.opts.Provider,
		BatchDelay: m.opts.BatchDelay, Logger: m.opts.Log, Workspace: c.Name, ExternalEmbedding: true,
		OnEmbeddingWork: func() { m.queue.wakeWorkspace(c.Name) }, Schema: sh.get})
	ix.SetSemanticPaused(!m.queue.setPolicy(c.Name, policy))
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
	w := &rpc.Workspace{Name: c.Name, Root: c.Root, Include: c.Include, Exclude: c.Exclude, SectionTokens: sectionTokens,
		EmbeddingPolicy: policy,
		EmbeddingMode: func() string {
			p, err := m.db.EmbeddingPolicy(context.Background(), id)
			if err != nil {
				return policy
			}
			return p
		},
		SectionSize: func() int {
			n, err := m.db.SectionTokens(context.Background(), id)
			if err != nil {
				return sectionTokens
			}
			return n
		},
		Index: ix, Docs: docs.New(ws.FS, ws.Matcher.Match), Following: f.Status, Warming: m.warmingOf(c.Name),
		Searched:       func() { m.queue.searchWorkspace(c.Name) },
		EmbeddingQueue: func() (string, string) { return m.queue.queueState(c.Name) },
		FrontmatterSchema: func() (*schema.Schema, rpc.SchemaStatus) {
			s, _ := sh.get()
			return s, sh.status()
		}}
	return &served{id: id, w: w, ctx: ctx, halt: halt, stop: stop}, nil
}

// schemaHolder is workspace id's schema holder, made on its first start.
func (m *Workspaces) schemaHolder(id int64, root string) *schemaHolder {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.schemas[id]
	if !ok {
		h = newSchemaHolder(root, func() { m.revalidate(id) })
		m.schemas[id] = h
	}
	return h
}

// revalidate queues the notes of workspace id that its new schema applies to.
func (m *Workspaces) revalidate(id int64) {
	m.mu.Lock()
	var ix *index.Indexer
	for _, s := range m.served {
		if s.id == id && s.stop != nil {
			ix = s.w.Index
		}
	}
	m.mu.Unlock()
	if ix == nil {
		return // not started yet: its first pass compares the new fingerprint itself
	}
	if err := ix.Revalidate(m.ctx); err != nil {
		logger.Warning(m.opts.Log, err, "revalidating notes after a schema change")
	}
}

// SetSchema names a workspace's frontmatter schema file ("" for none) and reads it at once: a valid
// one becomes active and revalidates the notes; an invalid one is reported, and the last valid
// schema stays active until the file is fixed.
func (m *Workspaces) SetSchema(ctx context.Context, name, path string) (rpc.SchemaStatus, error) {
	path = strings.TrimSpace(path)
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	s, ok := m.served[name]
	var h *schemaHolder
	if ok {
		h = m.schemas[s.id]
	}
	m.mu.Unlock()
	if !ok {
		return rpc.SchemaStatus{}, fmt.Errorf("%w: %s", store.ErrNoWorkspace, name)
	}
	if err := m.db.SetWorkspaceSchema(ctx, s.id, path); err != nil {
		return rpc.SchemaStatus{}, err
	}
	if h == nil {
		return rpc.SchemaStatus{Path: path}, nil // not served (its root is gone): read when it is
	}
	h.setPath(path)
	return h.status(), nil
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
	m.queue.stop()
	defer m.queue.start()
	m.opts.Provider = withProviderSlots(p, m.opts.MaxEmbedRequests)
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

// SetSectionTokens changes one workspace's chunk budget and reindexes that workspace.
func (m *Workspaces) SetSectionTokens(ctx context.Context, name string, tokens int) error {
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	s, ok := m.served[name]
	m.mu.Unlock()
	if !ok {
		return store.ErrNoWorkspace
	}
	if err := m.db.SetWorkspaceSectionTokens(ctx, s.id, tokens); err != nil {
		return err
	}
	if s.w.Index != nil {
		s.w.Index.Reindex("")
	}
	return nil
}

// SetPatterns validates and swaps a workspace's matcher, follower and indexer together.
func (m *Workspaces) SetPatterns(ctx context.Context, name string, include, exclude []string) error {
	if include == nil {
		include = []string{}
	}
	for _, pattern := range append(append([]string(nil), include...), exclude...) {
		if err := config.ValidPattern(pattern); err != nil {
			return fmt.Errorf("%w: pattern %q: %v", config.ErrInvalid, pattern, err)
		}
	}
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	old, ok := m.served[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", store.ErrNoWorkspace, name)
	}
	candidate := config.Workspace{Name: name, Root: old.w.Root, Include: include, Exclude: exclude}
	if err := checkRoot(candidate); err != nil {
		return err
	}
	if err := m.db.SetWorkspacePatterns(ctx, old.id, include, exclude); err != nil {
		return err
	}
	m.queue.stop()
	defer m.queue.start()
	if old.stop != nil {
		old.stop()
	}
	next, err := m.start(old.id, candidate)
	if err != nil {
		rollbackErr := m.db.SetWorkspacePatterns(context.WithoutCancel(ctx), old.id, old.w.Include, old.w.Exclude)
		restored := m.serve(old.id, config.Workspace{Name: name, Root: old.w.Root, Include: old.w.Include, Exclude: old.w.Exclude})
		m.mu.Lock()
		m.served[name] = restored
		m.mu.Unlock()
		return errors.Join(err, rollbackErr)
	}
	m.mu.Lock()
	m.served[name] = next
	m.mu.Unlock()
	return nil
}

// Focus gives the workspace the queue's next available batch. Calls from the
// TUI refresh a short lease; other clients keep using their search priority.
func (m *Workspaces) Focus(name string) error {
	m.mu.Lock()
	s, ok := m.served[name]
	m.mu.Unlock()
	if !ok || s.w.Index == nil {
		return store.ErrNoWorkspace
	}
	if m.queue.focusWorkspace(name) {
		s.w.Index.SetSemanticPaused(false)
	}
	return nil
}

// SetEmbeddingPolicy changes a workspace's queue admission and lexical gating.
func (m *Workspaces) SetEmbeddingPolicy(ctx context.Context, name, policy string) error {
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	s, ok := m.served[name]
	m.mu.Unlock()
	if !ok {
		return store.ErrNoWorkspace
	}
	if err := m.db.SetWorkspaceEmbeddingPolicy(ctx, s.id, policy); err != nil {
		return err
	}
	active := m.queue.setPolicy(name, policy)
	if s.w.Index != nil {
		s.w.Index.SetSemanticPaused(!active)
	}
	return nil
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
	m.queue.stop()
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
	m.queue.start()
	m.queue.wakeWorkspace(c.Name)
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
	w.Searched = func() { m.queue.searchWorkspace(to) }
	w.EmbeddingQueue = func() (string, string) { return m.queue.queueState(to) }
	m.queue.rename(name, to)
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
	m.queue.stop()
	defer m.queue.start()
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
		m.queue.forget(name)
		if h := m.schemas[s.id]; h != nil {
			h.stop()
			delete(m.schemas, s.id)
		}
	}
	return err
}

var _ rpc.Workspaces = (*Workspaces)(nil)
