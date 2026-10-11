package todo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/vfs"
)

// Roots is the daemon's workspaces: a workspace name's root, and every name.
type Roots interface {
	Root(workspace string) (root string, ok bool)
	Names() []string
}

// Filesystems opens directories on the daemon's disk. core/todo never touches the disk itself.
type Filesystems interface {
	// Open returns the filesystem rooted at dir, an absolute path to an existing directory, and
	// dir's canonical (symlink-resolved) path. A missing dir is an error wrapping vfs.ErrNotExist.
	Open(ctx context.Context, dir string) (vfs.FS, string, error)
}

// Manager is the todo system: every store's reads, writes and rules. It is safe for concurrent
// use; one mutex per store serializes everything done to that store.
type Manager struct {
	roots Roots
	disk  Filesystems
	j     Journal
	o     options

	mu           sync.Mutex
	stores       map[string]*store // open stores, by id
	blocked      map[string]string // store ids an ambiguous store move made read-only, and why
	movesSettled map[string]bool   // store ids whose interrupted store moves were settled
}

type options struct {
	now    func() time.Time
	log    logger.Logger
	home   string
	env    func(string) string
	events func(Event)
}

// Option configures New.
type Option func(*options)

// WithClock replaces time.Now: timestamps, the 28-day rule and the change log's times.
func WithClock(now func() time.Time) Option { return func(o *options) { o.now = now } }

// WithLogger sets where recovery and refresh report what they did. The default discards it.
func WithLogger(l logger.Logger) Option { return func(o *options) { o.log = l } }

// WithHome sets $HOME and what a leading ~ in a ref means.
func WithHome(home string) Option { return func(o *options) { o.home = home } }

// WithEnv sets where a $VAR that is neither built in nor the user's is looked up last.
func WithEnv(env func(string) string) Option { return func(o *options) { o.env = env } }

// WithEvents receives what the host records in sys.events beyond the verbs' own events.
func WithEvents(fn func(Event)) Option { return func(o *options) { o.events = fn } }

// New is the todo system over the daemon's workspaces, its disk and its journal.
func New(roots Roots, disk Filesystems, journal Journal, opts ...Option) *Manager {
	o := options{now: time.Now, log: logger.Nop{}}
	for _, opt := range opts {
		opt(&o)
	}
	return &Manager{roots: roots, disk: disk, j: journal, o: o, stores: map[string]*store{}, blocked: map[string]string{},
		movesSettled: map[string]bool{}}
}

func (m *Manager) now() time.Time { return m.o.now() }

func (m *Manager) event(e Event) {
	if m.o.events != nil {
		m.o.events(e)
	}
}

// At is a verb's first param: where its store is. A workspace name ("kb"), or {root = "/abs"} for
// a project that is not an AutoDoc workspace.
type At struct{ Workspace, Root string }

// DecodeParam reads a workspace name or a {root} map.
func (a *At) DecodeParam(v any) error {
	switch x := v.(type) {
	case string:
		*a = At{Workspace: x}
	case map[string]any:
		r, _ := x["root"].(string)
		*a = At{Root: r}
	case map[any]any:
		r, _ := x["root"].(string)
		*a = At{Root: r}
	default:
		return fmt.Errorf("%w: at: a workspace name or {root = \"/abs/path\"}", ErrInvalid)
	}
	if a.Workspace == "" && a.Root == "" {
		return fmt.Errorf("%w: at: a workspace name or {root = \"/abs/path\"}", ErrInvalid)
	}
	return nil
}

// ParamType is how a contract shows At.
func (At) ParamType() string { return "workspace | {root}" }

// where is an At resolved: the root, its store's record, and what may be done there.
type where struct {
	root      string // canonical
	workspace bool   // root is an AutoDoc workspace's
	rec       StoreRecord
	override  string
	writable  bool
	kbRoot    string
}

const todoDir = ".todo-list"

// resolve finds an At's store: the root's override, else <root>/.todo-list, at its canonical path.
// A store gets its stable id the first time a root resolves to it.
func (m *Manager) resolve(ctx context.Context, at At) (where, error) {
	var w where
	raw := at.Root
	switch {
	case at.Workspace != "" && at.Root != "":
		return w, fmt.Errorf("%w: at names a workspace or a root, not both", ErrInvalid)
	case at.Workspace != "":
		r, ok := m.roots.Root(at.Workspace)
		if !ok {
			return w, fmt.Errorf("%w: %s", ErrNoWorkspace, at.Workspace)
		}
		raw, w.workspace = r, true
	case !path.IsAbs(at.Root):
		return w, fmt.Errorf("%w: root %q is not an absolute path", ErrInvalid, at.Root)
	}
	fsys, canon, err := m.disk.Open(ctx, raw)
	if err != nil {
		if errors.Is(err, vfs.ErrNotExist) {
			return w, fmt.Errorf("%w: %s does not exist", ErrNotAStore, raw)
		}
		return w, err
	}
	defer fsys.Close()
	w.root = canon
	if !w.workspace {
		for _, name := range m.roots.Names() {
			if r, ok := m.roots.Root(name); ok && (path.Clean(r) == canon || path.Clean(r) == path.Clean(raw)) {
				w.workspace = true
			}
		}
	}
	st, err := m.j.Settings(ctx)
	if err != nil {
		return w, err
	}
	rr := st.Roots[canon]
	storePath := canon + "/" + todoDir
	if rr.Override != "" {
		storePath, w.override = rr.Override, rr.Override
	} else if sf, c, err := m.disk.Open(ctx, storePath); err == nil {
		storePath = c // a .todo-list that is a symlink resolves elsewhere, and is not writable
		sf.Close()
	}
	w.writable = (w.override != "") || (storePath == canon+"/"+todoDir && (w.workspace || !rr.Registered.IsZero()))
	if rec, ok := st.storeByPath(storePath); ok {
		w.rec = rec
	} else if w.rec, err = m.newStore(ctx, storePath); err != nil {
		return w, err
	}
	w.kbRoot = rr.KBRoot
	if w.workspace {
		if fi, err := fsys.Stat(ctx, "AGENTS.md"); err == nil && fi.IsRegular() {
			w.kbRoot = canon
		}
	}
	return w, nil
}

// newStore records the store at path with a fresh id, unless another call just did.
func (m *Manager) newStore(ctx context.Context, path string) (StoreRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rec StoreRecord
	err := m.j.UpdateSettings(ctx, func(s *Settings) error {
		if r, ok := s.storeByPath(path); ok {
			rec = r
			return nil
		}
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		rec = StoreRecord{ID: hex.EncodeToString(b[:]), Path: path, LastUse: m.now()}
		if s.Stores == nil {
			s.Stores = map[string]StoreRecord{}
		}
		s.Stores[rec.ID] = rec
		return nil
	})
	return rec, err
}

func (w where) resolved() Resolved {
	return Resolved{StoreID: w.rec.ID, Store: w.rec.Path, Root: w.root, Override: w.override, Writable: w.writable, Claimed: w.rec.Claimed}
}

// Resolve says where an At's store is, and whether the daemon may write it.
func (m *Manager) Resolve(ctx context.Context, at At) (Resolved, error) {
	w, err := m.resolve(ctx, at)
	return w.resolved(), err
}

// Register enrolls a project root that is not an AutoDoc workspace, so its .todo-list is a
// writable store. It writes no file, so it needs no claim. It reports whether the root was new.
func (m *Manager) Register(ctx context.Context, root string) (bool, error) {
	if !path.IsAbs(root) {
		return false, fmt.Errorf("%w: root %q is not an absolute path", ErrInvalid, root)
	}
	fsys, canon, err := m.disk.Open(ctx, root)
	if err != nil {
		return false, err
	}
	fsys.Close()
	added := false
	err = m.j.UpdateSettings(ctx, func(s *Settings) error {
		rr := s.Roots[canon]
		if rr.Registered.IsZero() {
			rr.Root, rr.Registered, added = canon, m.now(), true
			setRoot(s, rr)
		}
		return nil
	})
	if added {
		m.event(Event{Kind: "todos.register", Path: canon})
	}
	return added, err
}

func setRoot(s *Settings, rr RootRecord) {
	if s.Roots == nil {
		s.Roots = map[string]RootRecord{}
	}
	s.Roots[rr.Root] = rr
}

// SetDir points a root at another store directory, an existing one; "" restores
// <root>/.todo-list. It is the one verb where a client chooses a location.
func (m *Manager) SetDir(ctx context.Context, at At, dir string) (Resolved, error) {
	w, err := m.resolve(ctx, at)
	if err != nil {
		return Resolved{}, err
	}
	canonDir := ""
	if dir != "" {
		if !path.IsAbs(dir) {
			return Resolved{}, fmt.Errorf("%w: %q is not an absolute path", ErrInvalid, dir)
		}
		fsys, c, err := m.disk.Open(ctx, dir)
		if err != nil {
			return Resolved{}, fmt.Errorf("%w: %s must be an existing directory: %w", ErrInvalid, dir, err)
		}
		fsys.Close()
		canonDir = c
	}
	err = m.j.UpdateSettings(ctx, func(s *Settings) error {
		rr := s.Roots[w.root]
		rr.Root, rr.Override = w.root, canonDir
		setRoot(s, rr)
		return nil
	})
	if err != nil {
		return Resolved{}, err
	}
	m.event(Event{Kind: "todos.set_dir", Store: w.rec.ID, Path: w.root, Detail: canonDir})
	return m.Resolve(ctx, at)
}

// OpenOpts is what a client tells Open about a root that is not a workspace.
type OpenOpts struct {
	KBRoot string `json:"kb_root,omitempty"` // $KB_ROOT for its refs; never a write location
}

// Open checks that an At's store exists and records the client's context for its root.
func (m *Manager) Open(ctx context.Context, at At, opts OpenOpts) (Opened, error) {
	w, err := m.resolve(ctx, at)
	if err != nil {
		return Opened{}, err
	}
	if opts.KBRoot != "" {
		if !path.IsAbs(opts.KBRoot) {
			return Opened{}, fmt.Errorf("%w: kb_root %q is not an absolute path", ErrInvalid, opts.KBRoot)
		}
		err := m.j.UpdateSettings(ctx, func(s *Settings) error {
			rr := s.Roots[w.root]
			rr.Root, rr.KBRoot = w.root, path.Clean(opts.KBRoot)
			setRoot(s, rr)
			return nil
		})
		if err != nil {
			return Opened{}, err
		}
	}
	st, err := m.open(ctx, w)
	if err != nil {
		return Opened{}, err
	}
	m.touch(ctx, st.id)
	return Opened{StoreID: st.id, Store: st.path}, nil
}

func (m *Manager) touch(ctx context.Context, id string) {
	err := m.j.UpdateSettings(ctx, func(s *Settings) error {
		if rec, ok := s.Stores[id]; ok {
			rec.LastUse = m.now()
			s.Stores[id] = rec
		}
		return nil
	})
	if err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.touch.failed", "store": id})
	}
}

// Stores lists every store the daemon knows, with the roots that resolve to it.
func (m *Manager) Stores(ctx context.Context) ([]StoreInfo, error) {
	st, err := m.j.Settings(ctx)
	if err != nil {
		return nil, err
	}
	var out []StoreInfo
	for _, rec := range st.Stores {
		info := StoreInfo{ID: rec.ID, Path: rec.Path, Claimed: rec.Claimed, LastUse: rec.LastUse, Roots: []string{}}
		for _, root := range slices.Sorted(maps.Keys(st.Roots)) {
			rr := st.Roots[root]
			if rr.Override == rec.Path || (rr.Override == "" && root+"/"+todoDir == rec.Path) {
				info.Roots = append(info.Roots, root)
			}
		}
		if len(info.Roots) == 0 && strings.HasSuffix(rec.Path, "/"+todoDir) {
			info.Roots = append(info.Roots, strings.TrimSuffix(rec.Path, "/"+todoDir))
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b StoreInfo) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// ClaimOpts is what the cutover's claim may do besides claiming. The zero value does nothing more.
type ClaimOpts struct {
	// LegacyWorkspace is the directory auto-core's $WORKSPACE meant when it wrote the store's refs:
	// Neovim's project root, not the store's workspace. The claim rewrites each adr and review ref
	// $WORKSPACE/<rest> that resolves under it and not under the store's own $WORKSPACE.
	LegacyWorkspace string `json:"legacy_workspace,omitempty"`
}

// Claim lets the daemon write an At's store: the cutover. The claim is the daemon's record, by the
// store's id, so it holds before the store's directory exists; the .autodoc-owner marker is
// written whenever the directory exists, here or by Scaffold. With a LegacyWorkspace, the claim
// then rewrites the store's legacy $WORKSPACE refs once (cutover), archives included.
func (m *Manager) Claim(ctx context.Context, at At, opts ...ClaimOpts) error {
	var o ClaimOpts
	for _, x := range opts {
		if x.LegacyWorkspace != "" {
			o = x
		}
	}
	if o.LegacyWorkspace != "" && (!path.IsAbs(o.LegacyWorkspace) || path.Clean(o.LegacyWorkspace) != o.LegacyWorkspace) {
		return fmt.Errorf("%w: legacy_workspace %q is not a clean absolute path", ErrInvalid, o.LegacyWorkspace)
	}
	w, err := m.resolve(ctx, at)
	if err != nil {
		return err
	}
	if !w.writable {
		return fmt.Errorf("%w: %s", ErrNotWritable, w.rec.Path)
	}
	err = m.j.UpdateSettings(ctx, func(s *Settings) error {
		rec := s.Stores[w.rec.ID]
		if !rec.Claimed {
			rec.Claimed, rec.ClaimedAt = true, m.now()
			s.Stores[w.rec.ID] = rec
		}
		return nil
	})
	if err != nil {
		return err
	}
	if st, err := m.open(ctx, w); err == nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		if err := st.mark(ctx, m.now()); err != nil {
			return err
		}
		if o.LegacyWorkspace == "" {
			return nil
		}
		if err := m.ready(ctx, st); err != nil {
			return err
		}
		return m.cutover(ctx, st, w, o.LegacyWorkspace)
	}
	return nil // no directory yet: Scaffold writes the marker
}

// Release makes an At's store read-only to the daemon again: the cutover's rollback.
func (m *Manager) Release(ctx context.Context, at At) error {
	w, err := m.resolve(ctx, at)
	if err != nil {
		return err
	}
	err = m.j.UpdateSettings(ctx, func(s *Settings) error {
		rec := s.Stores[w.rec.ID]
		rec.Claimed, rec.ClaimedAt = false, time.Time{}
		s.Stores[w.rec.ID] = rec
		return nil
	})
	if err != nil {
		return err
	}
	if st, err := m.open(ctx, w); err == nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.unmark(ctx)
	}
	return nil
}

// writable resolves an At for a write: a writable, claimed store that no ambiguous move blocks.
func (m *Manager) writable(ctx context.Context, at At) (where, error) {
	w, err := m.resolve(ctx, at)
	if err != nil {
		return w, err
	}
	if !w.writable {
		return w, fmt.Errorf("%w: %s", ErrNotWritable, w.rec.Path)
	}
	if !w.rec.Claimed {
		return w, fmt.Errorf("%w: %s", ErrUnclaimed, w.rec.Path)
	}
	if err := m.recoverMoves(ctx, w.rec.ID, false); err != nil {
		return w, err
	}
	m.mu.Lock()
	why := m.blocked[w.rec.ID]
	m.mu.Unlock()
	if why != "" {
		return w, fmt.Errorf("%w: %s: %s", ErrNotWritable, w.rec.Path, why)
	}
	return w, nil
}

// Scaffold creates an At's store directory and its buckets, and the owner marker. It is a write:
// the store must be writable and claimed.
func (m *Manager) Scaffold(ctx context.Context, at At) (Opened, error) {
	w, err := m.writable(ctx, at)
	if err != nil {
		return Opened{}, err
	}
	made := false
	if fsys, _, err := m.disk.Open(ctx, w.rec.Path); err != nil {
		if !errors.Is(err, vfs.ErrNotExist) {
			return Opened{}, err
		}
		parent, base := path.Split(w.rec.Path)
		pfs, _, err := m.disk.Open(ctx, path.Clean(parent))
		if err != nil {
			return Opened{}, fmt.Errorf("%w: the store's parent directory: %w", ErrNotAStore, err)
		}
		err = pfs.MkdirAll(ctx, base)
		pfs.Close()
		if err != nil {
			return Opened{}, err
		}
		made = true
	} else {
		fsys.Close()
	}
	st, err := m.open(ctx, w)
	if err != nil {
		return Opened{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, b := range Statuses {
		if _, err := st.fsys.Stat(ctx, b); errors.Is(err, vfs.ErrNotExist) {
			if err := st.fsys.MkdirAll(ctx, b); err != nil {
				return Opened{}, err
			}
			made = true
		}
	}
	if err := st.mark(ctx, m.now()); err != nil {
		return Opened{}, err
	}
	return Opened{StoreID: st.id, Store: st.path, Scaffolded: made}, nil
}

// Vars lists the variables an At's refs may use: its built-ins, then the user's.
func (m *Manager) Vars(ctx context.Context, at At) ([]Var, error) {
	w, err := m.resolve(ctx, at)
	if err != nil {
		return nil, err
	}
	sc, err := m.scope(ctx, w)
	if err != nil {
		return nil, err
	}
	out := []Var{{Name: VarKBRoot, Value: sc.kbRoot, Builtin: true}, {Name: VarWorkspace, Value: sc.workspace, Builtin: true},
		{Name: VarHome, Value: sc.home, Builtin: true}}
	for _, k := range slices.Sorted(maps.Keys(sc.user)) {
		out = append(out, Var{Name: k, Value: sc.user[k]})
	}
	return out, nil
}

// SetVar sets a user variable, or clears it with "". Built-ins cannot be set.
func (m *Manager) SetVar(ctx context.Context, name, value string) error {
	if !varNameRe.MatchString(name) {
		return fmt.Errorf("%w: %q is not a variable name (letters, digits and _, not starting with a digit)", ErrInvalid, name)
	}
	if isBuiltin(name) {
		return fmt.Errorf("%w: %s is built in and resolves by itself", ErrInvalid, name)
	}
	return m.j.UpdateSettings(ctx, func(s *Settings) error {
		if s.Vars == nil {
			s.Vars = map[string]string{}
		}
		if value == "" {
			delete(s.Vars, name)
		} else {
			s.Vars[name] = value
		}
		return nil
	})
}

// scope is what a store's refs resolve against.
func (m *Manager) scope(ctx context.Context, w where) (scope, error) {
	st, err := m.j.Settings(ctx)
	if err != nil {
		return scope{}, err
	}
	return scope{kbRoot: w.kbRoot, workspace: w.root, home: m.o.home, user: st.Vars, env: m.o.env}, nil
}

// withExists gives sc a stat of absolute ref paths, through the disk; done releases it.
func (m *Manager) withExists(ctx context.Context, sc scope) (scope, func()) {
	var root vfs.FS
	sc.exists = func(abs string) bool {
		if root == nil {
			fsys, _, err := m.disk.Open(ctx, "/")
			if err != nil {
				return false
			}
			root = fsys
		}
		name := strings.TrimPrefix(path.Clean(abs), "/")
		if name == "" {
			name = "."
		}
		_, err := root.Stat(ctx, name)
		return err == nil
	}
	return sc, func() {
		if root != nil {
			root.Close()
		}
	}
}
