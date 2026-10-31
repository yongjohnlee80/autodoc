package todo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/paths"
)

// A WORKSPACE'S VARIABLES (docs/decisions/todos.md#variables-belong-to-the-workspace). Variables
// belong to the workspace, the KB, not to a todo store: a store belongs to exactly one workspace
// and reads that workspace's variables, and so does every other document of the workspace. A name's
// value is, first match winning: this machine's row for the workspace (absolute, never travels);
// the KB's file, <root>/.autodoc-vars.toml (relative paths only, carried by git, resolved from each
// machine's KB root); the derived built-in ($KB_ROOT, the workspace root, and $HOME); a global user
// variable; the environment. A name a pending migration holds in conflict answers only to this
// machine's row.

// varsFile is the variable file: a KB's, at its root. A store's own
// (docs/decisions/todos.md#one-answer-from-every-route-to-a-store) is read only by
// the migration that folds it into its workspace's (fold).
const varsFile = ".autodoc-vars.toml"

// VarsFile is the name of a KB's variable file, at its root: what a reader of the variables watches.
const VarsFile = varsFile

// migratedSuffix is added to a store's variable file once the migration has carried every value of it.
const migratedSuffix = ".migrated"

// maxVarsFile is the largest variable file read: a larger one is refused whole, never cut short.
const maxVarsFile = 64 << 10

// VarProjectRoot is the project a workspace serves: what AutoVim's $WORKSPACE meant. It is a user
// variable, set per KB (relative, in the KB's file) or per machine.
const VarProjectRoot = "PROJECT_ROOT"

// storeVars is a variable file as read: its variables, by name, as written, and the names whose
// value is set per machine.
type storeVars struct {
	Vars    map[string]string
	Machine []string
}

// varsDoc is the file's TOML: a top-level machine list and a [vars] table, nothing else.
type varsDoc struct {
	Machine []string          `toml:"machine"`
	Vars    map[string]string `toml:"vars"`
}

// parseStoreVars reads a variable file. A key or table the format does not have, a name that is not
// a variable name, or a name both machine-only and given a value here is refused whole: the file is
// then read as absent.
func parseStoreVars(b []byte) (storeVars, error) {
	var d varsDoc
	md, err := toml.NewDecoder(bytes.NewReader(b)).Decode(&d)
	if err != nil {
		return storeVars{}, err
	}
	if u := md.Undecoded(); len(u) > 0 {
		return storeVars{}, fmt.Errorf("unknown key %s: only a top-level machine list and a [vars] table", u[0])
	}
	sv := storeVars{Vars: map[string]string{}, Machine: d.Machine}
	for _, n := range d.Machine {
		if !varNameRe.MatchString(n) {
			return storeVars{}, fmt.Errorf("machine: %q is not a variable name", n)
		}
	}
	for k, v := range d.Vars {
		switch {
		case !varNameRe.MatchString(k):
			return storeVars{}, fmt.Errorf("vars: %q is not a variable name", k)
		case slices.Contains(d.Machine, k):
			return storeVars{}, fmt.Errorf("vars: %s is machine-only, so its value is set per machine, not here", k)
		case v == "":
			return storeVars{}, fmt.Errorf("vars: %s is empty", k)
		}
		sv.Vars[k] = v
	}
	return sv, nil
}

// clone is a copy of sv to change without changing sv.
func (sv storeVars) clone() storeVars {
	c := storeVars{Vars: maps.Clone(sv.Vars), Machine: slices.Clone(sv.Machine)}
	if c.Vars == nil {
		c.Vars = map[string]string{}
	}
	return c
}

// undoDeclare undoes, in sv (the file now), what declaring name machine-only changed in prior (the
// file then): the portable value it dropped comes back, unless the file has one again, and a
// declaration it added goes. Everything else in sv stays as it is now.
func (prior storeVars) undoDeclare(sv *storeVars, name string) {
	v, had := prior.Vars[name]
	if !had && slices.Contains(prior.Machine, name) {
		return // declared already: the call changed nothing
	}
	sv.Machine = slices.DeleteFunc(sv.Machine, func(n string) bool { return n == name })
	if _, again := sv.Vars[name]; had && !again {
		sv.Vars[name] = v
	}
}

// render writes sv as a KB's file: the machine list, then [vars], each sorted, so the file a verb
// writes is stable and diffs cleanly.
func (sv storeVars) render() []byte {
	var b strings.Builder
	b.WriteString("# AutoDoc variables of this KB. Values are paths relative to this\n")
	b.WriteString("# directory, resolved on each machine; a machine's own values are set per machine.\n")
	b.WriteString("# machine: the names whose value is set per machine, never here.\n")
	if len(sv.Machine) > 0 {
		names := slices.Clone(sv.Machine)
		sort.Strings(names)
		b.WriteString("machine = [")
		for i, n := range names {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", n)
		}
		b.WriteString("]\n")
	}
	if len(sv.Vars) > 0 {
		b.WriteString("\n[vars]\n")
		for _, k := range slices.Sorted(maps.Keys(sv.Vars)) {
			fmt.Fprintf(&b, "%s = %q\n", k, sv.Vars[k])
		}
	}
	return []byte(b.String())
}

// isAbsValue reports a value that names a machine's path, not one relative to the KB: absolute, or
// under ~.
func isAbsValue(v string) bool { return strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~") }

// readVarsFile reads the variable file in dir. ok is false for an absent file or directory; a file
// that cannot be read or parsed is its error.
func (m *Manager) readVarsFile(ctx context.Context, dir string) (sv storeVars, ok bool, err error) {
	fsys, _, err := m.disk.Open(ctx, dir)
	if err != nil {
		if errors.Is(err, vfs.ErrNotExist) {
			return storeVars{}, false, nil
		}
		return storeVars{}, false, err
	}
	defer fsys.Close()
	r, err := fsys.Open(ctx, varsFile, 0)
	if err != nil {
		if errors.Is(err, vfs.ErrNotExist) {
			return storeVars{}, false, nil
		}
		return storeVars{}, false, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, maxVarsFile+1))
	if err != nil {
		return storeVars{}, false, err
	}
	if len(b) > maxVarsFile {
		return storeVars{}, false, fmt.Errorf("%s/%s: larger than %d bytes", dir, varsFile, maxVarsFile)
	}
	sv, err = parseStoreVars(b)
	if err != nil {
		return storeVars{}, false, fmt.Errorf("%s/%s: %w", dir, varsFile, err)
	}
	return sv, true, nil
}

// WHICH WORKSPACE (docs/decisions/todos.md#which-workspace-a-store-belongs-to)

// containing is the root of the deepest workspace dir lies in, "" for none.
func (m *Manager) containing(dir string) string {
	best := ""
	for _, name := range m.roots.Names() {
		if r, ok := m.roots.Root(name); ok && paths.Under(dir, path.Clean(r)) && len(r) > len(best) {
			best = path.Clean(r)
		}
	}
	return best
}

// nameOf is the name of the workspace whose root is root, "" for none.
func (m *Manager) nameOf(root string) string {
	for _, name := range m.roots.Names() {
		if r, ok := m.roots.Root(name); ok && path.Clean(r) == root {
			return name
		}
	}
	return ""
}

// ownerOf is the workspace root a store record belongs to now: the recorded one while a workspace has
// that root; else the deepest workspace containing the store; else "". A recorded root whose
// workspace was removed is kept, so adding the workspace back restores the link (a todos.set_kb one
// to an outside store included); until then the store reads as an unrecorded one, and nothing is
// recorded for it.
func (m *Manager) ownerOf(rec StoreRecord) string {
	if rec.KB != "" && m.nameOf(rec.KB) != "" {
		return rec.KB
	}
	return m.containing(rec.Path)
}

// owner is the root of the workspace the store id belongs to: ownerOf's. A store recorded before
// stores belonged to their workspace (docs/decisions/todos.md#which-workspace-a-store-belongs-to),
// or outside every workspace then, has its containing workspace recorded now, so a workspace added
// later never takes it.
func (m *Manager) owner(ctx context.Context, id string) (string, error) {
	st, err := m.j.Settings(ctx)
	if err != nil {
		return "", err
	}
	rec, ok := st.Stores[id]
	if !ok {
		return "", nil
	}
	if rec.KB != "" {
		return m.ownerOf(rec), nil
	}
	kb := m.containing(rec.Path)
	if kb == "" {
		return "", nil
	}
	err = m.j.UpdateSettings(ctx, func(s *Settings) error {
		r, ok := s.Stores[id]
		if !ok || r.KB != "" {
			return nil
		}
		r.KB = kb
		s.Stores[id] = r
		return nil
	})
	return kb, err
}

// SetKB records which workspace an At's store belongs to (todos.set_kb): a store outside every
// workspace is linked to one, and a store may be moved to another. Its refs resolve with the new
// workspace's variables from now on.
func (m *Manager) SetKB(ctx context.Context, at At, workspace string) error {
	root, ok := m.roots.Root(workspace)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoWorkspace, workspace)
	}
	w, err := m.resolve(ctx, at)
	if err != nil {
		return err
	}
	root = path.Clean(root)
	err = m.j.UpdateSettings(ctx, func(s *Settings) error {
		r, ok := s.Stores[w.rec.ID]
		if !ok {
			return fmt.Errorf("%w: store %s", ErrNotFound, w.rec.ID)
		}
		r.KB = root
		s.Stores[w.rec.ID] = r
		return nil
	})
	if err == nil {
		m.unfold(root)
		m.event(Event{Kind: "todos.set_kb", Store: w.rec.ID, Path: w.rec.Path, Detail: "workspace " + workspace + " (" + root + ")"})
		m.storesChanged()
	}
	return err
}

// Owner is who a file at abs belongs to (workspace.of): the one owner rule the index and todos.vars
// use. A file in a known todo store belongs to the store's workspace (recorded, or containing);
// any other file to the deepest workspace containing it; otherwise to none ("", "").
func (m *Manager) Owner(ctx context.Context, abs string) (workspace, root string, err error) {
	abs = path.Clean(abs)
	st, err := m.j.Settings(ctx)
	if err != nil {
		return "", "", err
	}
	var best StoreRecord
	for _, rec := range st.Stores {
		if paths.Under(abs, rec.Path) && len(rec.Path) > len(best.Path) {
			best = rec
		}
	}
	if best.ID != "" {
		if root, err = m.owner(ctx, best.ID); err != nil {
			return "", "", err
		}
	} else {
		root = m.containing(abs)
	}
	if root == "" {
		return "", "", nil
	}
	return m.nameOf(root), root, nil
}

// THE LAYERS

// layers are a workspace's variables before precedence.
type layers struct {
	kb       string            // the workspace's root; "" for a store that belongs to none
	machine  map[string]string // this machine's rows for the workspace
	file     storeVars         // the KB's file, its refused values left out; empty when absent or refused
	fileErr  error             // why the file was refused; nil when read or absent
	refused  []string          // names the file gave an absolute value: absolute paths belong to a machine
	derived  map[string]string // the built-ins
	global   map[string]string
	conflict map[string]bool // names a pending migration holds in conflict
}

// layersOf reads the variables of the workspace whose root is kb ("" for none), folding its stores'
// old files in first (fold). A refused file is reported once per distinct error.
func (m *Manager) layersOf(ctx context.Context, kb string) (layers, error) {
	l := layers{kb: kb, derived: map[string]string{VarHome: m.o.home}}
	if kb != "" {
		l.derived[VarKBRoot] = kb
		l.conflict = m.fold(ctx, kb)
		var ok bool
		l.file, ok, l.fileErr = m.readVarsFile(ctx, kb)
		m.reportVarsFile("kb:"+kb, kb, l.fileErr)
		if ok {
			for _, n := range slices.Sorted(maps.Keys(l.file.Vars)) {
				if isAbsValue(l.file.Vars[n]) {
					l.refused = append(l.refused, n)
					delete(l.file.Vars, n)
				}
			}
			m.reportRefused(kb, l.refused)
		}
	}
	st, err := m.j.Settings(ctx)
	if err != nil {
		return l, err
	}
	l.machine, l.global = st.WorkspaceVars[kb], st.Vars
	return l, nil
}

// layersAt reads the variables of w's store: its workspace's.
func (m *Manager) layersAt(ctx context.Context, w where) (layers, error) {
	kb, err := m.owner(ctx, w.rec.ID)
	if err != nil {
		return layers{}, err
	}
	return m.layersOf(ctx, kb)
}

// value is name's effective value and where it came from
// (docs/decisions/todos.md#where-a-variables-value-comes-from).
func (l layers) value(name string, env func(string) string) (string, string) {
	if v := l.machine[name]; v != "" {
		return v, "machine"
	}
	if l.conflict[name] {
		return "", "conflict"
	}
	if v := l.file.Vars[name]; v != "" {
		return path.Join(l.kb, v), "kb"
	}
	if isBuiltin(name) {
		if v := l.derived[name]; v != "" {
			return v, "derived"
		}
		return "", ""
	}
	if v := l.global[name]; v != "" {
		return v, "global"
	}
	if env != nil {
		if v := env(name); v != "" {
			return v, "env"
		}
	}
	return "", ""
}

// names are every variable the layers name, the built-ins first, then the rest sorted.
func (l layers) names() []string {
	seen := map[string]bool{}
	var rest []string
	add := func(n string) {
		if !seen[n] && !isBuiltin(n) {
			seen[n] = true
			rest = append(rest, n)
		}
	}
	for n := range l.machine {
		add(n)
	}
	for n := range l.file.Vars {
		add(n)
	}
	for _, n := range l.file.Machine {
		add(n)
	}
	for _, n := range l.refused {
		add(n)
	}
	for n := range l.conflict {
		add(n)
	}
	for n := range l.global {
		add(n)
	}
	sort.Strings(rest)
	return append([]string{VarKBRoot, VarHome}, rest...)
}

// scopeOf is what refs of the workspace kb resolve against.
func (m *Manager) scopeOf(l layers) scope {
	get := func(n string) string { v, _ := l.value(n, nil); return v }
	sc := scope{kbRoot: get(VarKBRoot), workspace: get(VarWorkspace), projectRoot: get(VarProjectRoot), home: get(VarHome),
		user: map[string]string{}, machine: map[string]bool{}, conflict: l.conflict, env: m.o.env}
	for _, n := range l.names() {
		if !isBuiltin(n) {
			if v := get(n); v != "" {
				sc.user[n] = v
			}
		}
	}
	for _, n := range l.file.Machine {
		sc.machine[n] = true
	}
	return sc
}

// scope is w's store's scope: its workspace's, the same from every route.
func (m *Manager) scope(ctx context.Context, w where) (scope, error) {
	l, err := m.layersAt(ctx, w)
	if err != nil {
		return scope{}, err
	}
	sc := m.scopeOf(l)
	sc.storeHome = path.Dir(w.rec.Path)
	return sc, nil
}

// list is the layers as todos.vars and workspace.vars answer them.
func (m *Manager) list(l layers) []Var {
	machine := map[string]bool{}
	for _, n := range l.file.Machine {
		machine[n] = true
	}
	var out []Var
	for _, n := range l.names() {
		v, src := l.value(n, m.o.env)
		out = append(out, Var{Name: n, Value: v, Source: src, Builtin: isBuiltin(n), Derived: l.derived[n],
			Machine: machine[n], Conflict: l.conflict[n]})
	}
	return out
}

// Vars lists the variables an At's store's refs may use: its workspace's, the same from every route.
func (m *Manager) Vars(ctx context.Context, at At) ([]Var, error) {
	w, err := m.resolve(ctx, at)
	if err != nil {
		return nil, err
	}
	l, err := m.layersAt(ctx, w)
	if err != nil {
		return nil, err
	}
	return m.list(l), nil
}

// WorkspaceVars lists a workspace's variables (workspace.vars). "" is no workspace's: the ones a
// path in none resolves its refs with (docs/decisions/todos.md#resolving-refs-in-every-document,
// the owner rule's third case), $HOME and the global variables, before the environment, as the
// index resolves such a path (AllEffectiveVars' "").
func (m *Manager) WorkspaceVars(ctx context.Context, workspace string) ([]Var, error) {
	kb := ""
	if workspace != "" {
		root, ok := m.roots.Root(workspace)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNoWorkspace, workspace)
		}
		kb = path.Clean(root)
	}
	l, err := m.layersOf(ctx, kb)
	if err != nil {
		return nil, err
	}
	return m.list(l), nil
}

// EffectiveVars are the values a document of the workspace rooted at kb resolves $NAME refs with: each
// name that has one, built-ins included. The index reads them for every document.
func (m *Manager) EffectiveVars(ctx context.Context, kb string) (map[string]string, error) {
	l, err := m.layersOf(ctx, kb)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, n := range l.names() {
		if v, _ := l.value(n, m.o.env); v != "" {
			out[n] = v
		}
	}
	return out, nil
}

// AllEffectiveVars are EffectiveVars for every workspace and every store's, by root, "" holding
// those of a path in no workspace: what the index resolves each document's refs with.
func (m *Manager) AllEffectiveVars(ctx context.Context) (map[string]map[string]string, error) {
	roots := map[string]bool{"": true}
	for _, name := range m.roots.Names() {
		if r, ok := m.roots.Root(name); ok {
			roots[path.Clean(r)] = true
		}
	}
	st, err := m.j.Settings(ctx)
	if err != nil {
		return nil, err
	}
	for _, rec := range st.Stores {
		roots[m.ownerOf(rec)] = true
	}
	out := map[string]map[string]string{}
	for _, root := range slices.Sorted(maps.Keys(roots)) {
		vs, err := m.EffectiveVars(ctx, root)
		if err != nil {
			return nil, err
		}
		out[root] = vs
	}
	return out, nil
}

// SETTING A VARIABLE

// Var scopes: where a value is kept.
const (
	ScopeGlobal  = "global"  // the daemon's user vars, for every workspace; never a built-in
	ScopeKB      = "kb"      // the KB's file: a path relative to the KB root, carried by git
	ScopeStore   = "store"   // todos.set_var's name for ScopeKB: the store's workspace's file
	ScopeMachine = "machine" // this machine's row for the workspace
)

// SetVarOpts says where SetVar keeps a value: a workspace, named or an At's store's, in its KB file or
// for this machine; or, with neither, the global user vars.
type SetVarOpts struct {
	At        *At    `json:"at,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Scope     string `json:"scope,omitempty"` // "global" (the default with neither), "kb" or "store", or "machine" (the default with a workspace)
	// MachineOnly declares the name machine-only in the KB's file with a machine value: other machines
	// then report it as a value to set, never a ref error, and a migration conflict on it is settled.
	MachineOnly bool `json:"machine_only,omitempty"`
}

// SetVar sets a variable, or clears it with "". A global var is any name but a built-in's; a KB or
// machine value may override a built-in.
func (m *Manager) SetVar(ctx context.Context, name, value string, opts ...SetVarOpts) error {
	if !varNameRe.MatchString(name) {
		return fmt.Errorf("%w: %q is not a variable name (letters, digits and _, not starting with a digit)", ErrInvalid, name)
	}
	var o SetVarOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	scope := o.Scope
	switch {
	case scope != "":
	case o.Workspace != "":
		scope = ScopeMachine
	case o.At != nil:
		scope = ScopeStore
	default:
		scope = ScopeGlobal
	}
	if scope == ScopeGlobal {
		if isBuiltin(name) {
			return fmt.Errorf("%w: %s is derived per workspace; override it for a workspace, not globally", ErrInvalid, name)
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
	kb, err := m.kbFor(ctx, o)
	if err != nil {
		return err
	}
	m.varsMu.Lock()
	defer m.varsMu.Unlock()
	defer m.unfold(kb)
	switch scope {
	case ScopeKB, ScopeStore:
		if o.MachineOnly {
			return fmt.Errorf("%w: machine_only goes with scope \"machine\"", ErrInvalid)
		}
		rel := ""
		if strings.HasPrefix(value, "~") {
			return fmt.Errorf("%w: %s is under this machine's home: set it with scope \"machine\"", ErrInvalid, value)
		}
		if value != "" {
			rel = relTo(kb, m.absValue(kb, value)) // absolute both, so always relative: "../…" outside the KB
		}
		return m.editKBFile(ctx, kb, func(sv *storeVars) error {
			if slices.Contains(sv.Machine, name) && value != "" {
				return fmt.Errorf("%w: %s is machine-only in this KB; set it with scope \"machine\"", ErrInvalid, name)
			}
			if value == "" {
				delete(sv.Vars, name)
			} else {
				sv.Vars[name] = rel
			}
			return nil
		})
	case ScopeMachine:
		// a machine-only declaration goes in the KB's file first, so no machine without the value
		// errs, and a file edit that fails leaves no row behind; what the declaration changed is
		// undone if the row then cannot be kept, and only that: an edit the file took in between stays
		var prior *storeVars
		if o.MachineOnly {
			err := m.editKBFile(ctx, kb, func(sv *storeVars) error {
				was := sv.clone()
				prior = &was
				if !slices.Contains(sv.Machine, name) {
					sv.Machine = append(sv.Machine, name)
				}
				delete(sv.Vars, name)
				return nil
			})
			if err != nil {
				return err
			}
		}
		abs := ""
		if value != "" {
			abs = m.absValue(kb, value)
		}
		err := m.j.UpdateSettings(ctx, func(s *Settings) error {
			if s.WorkspaceVars == nil {
				s.WorkspaceVars = map[string]map[string]string{}
			}
			vals := maps.Clone(s.WorkspaceVars[kb])
			if vals == nil {
				vals = map[string]string{}
			}
			if abs == "" {
				delete(vals, name)
			} else {
				vals[name] = abs
			}
			if len(vals) == 0 {
				delete(s.WorkspaceVars, kb)
			} else {
				s.WorkspaceVars[kb] = vals
			}
			return nil
		})
		if err != nil && prior != nil {
			if rerr := m.editKBFile(ctx, kb, func(sv *storeVars) error { prior.undoDeclare(sv, name); return nil }); rerr != nil {
				return errors.Join(err, fmt.Errorf("undoing the machine-only declaration of %s: %w", name, rerr))
			}
		}
		if err == nil {
			m.storesChanged()
		}
		return err
	}
	return fmt.Errorf("%w: scope %q: global, kb (or store) or machine", ErrInvalid, scope)
}

// kbFor is the workspace root a SetVar names: its workspace, or its At's store's workspace.
func (m *Manager) kbFor(ctx context.Context, o SetVarOpts) (string, error) {
	switch {
	case o.Workspace != "" && o.At != nil:
		return "", fmt.Errorf("%w: name a workspace or an at, not both", ErrInvalid)
	case o.Workspace != "":
		root, ok := m.roots.Root(o.Workspace)
		if !ok {
			return "", fmt.Errorf("%w: %s", ErrNoWorkspace, o.Workspace)
		}
		return path.Clean(root), nil
	case o.At != nil:
		w, err := m.writable(ctx, *o.At)
		if err != nil {
			return "", err
		}
		kb, err := m.owner(ctx, w.rec.ID)
		if err == nil && kb == "" {
			err = fmt.Errorf("%w: the store %s belongs to no workspace: link it with todos.set_kb", ErrInvalid, w.rec.Path)
		}
		return kb, err
	}
	return "", fmt.Errorf("%w: a kb or machine value names a workspace or an at", ErrInvalid)
}

// absValue is a value as an absolute path: ~ expanded, a relative one the KB root's.
func (m *Manager) absValue(kb, v string) string {
	switch {
	case v == "~" || strings.HasPrefix(v, "~/"):
		return path.Join(m.o.home, strings.TrimPrefix(v, "~"))
	case strings.HasPrefix(v, "/"):
		return path.Clean(v)
	}
	return path.Join(kb, v)
}

// editKBFile changes the KB's file at kb through fn, written atomically at the version it was read
// at; a file left with nothing is removed. A refused file is never rewritten.
func (m *Manager) editKBFile(ctx context.Context, kb string, fn func(*storeVars) error) error {
	fsys, _, err := m.disk.Open(ctx, kb)
	if err != nil {
		return err
	}
	defer fsys.Close()
	d := docs.New(fsys, func(p string) bool { return p == varsFile })
	var ver vfs.Version
	sv := storeVars{Vars: map[string]string{}}
	doc, err := d.Read(ctx, varsFile)
	switch {
	case errors.Is(err, vfs.ErrNotExist):
	case err != nil:
		return err
	default:
		ver = doc.Version
		if sv, err = parseStoreVars(doc.Content); err != nil {
			return fmt.Errorf("%w: %s/%s is not a variable file the daemon rewrites (fix it by hand): %v", ErrInvalid, kb, varsFile, err)
		}
	}
	if err := fn(&sv); err != nil {
		return err
	}
	if len(sv.Vars) == 0 && len(sv.Machine) == 0 {
		if ver == "" {
			return nil
		}
		err := d.Remove(ctx, varsFile, ver)
		if err == nil {
			m.storesChanged()
		}
		return err
	}
	_, err = d.Write(ctx, varsFile, sv.render(), ver)
	if err == nil {
		m.event(Event{Kind: "todos.vars", Path: kb + "/" + varsFile})
		m.storesChanged()
	}
	return err
}

// reportVarsFile reports a refused variable file once per distinct error, and its recovery. key names
// the file's owner: "kb:<root>" for a KB's, a store id for a store's.
func (m *Manager) reportVarsFile(key, dir string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.mu.Lock()
	if m.varsErr == nil {
		m.varsErr = map[string]string{}
	}
	prev, seen := m.varsErr[key]
	m.varsErr[key] = msg
	m.mu.Unlock()
	store := ""
	if !strings.HasPrefix(key, "kb:") {
		store = key
	}
	if msg != "" && msg != prev {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_invalid", "path": dir + "/" + varsFile})
		m.event(Event{Kind: "todos.vars_invalid", Store: store, Path: dir + "/" + varsFile, Detail: msg})
	} else if msg == "" && seen && prev != "" {
		m.event(Event{Kind: "todos.vars_valid", Store: store, Path: dir + "/" + varsFile})
	}
}

// reportRefused reports, once per distinct list, the names a KB's file gave absolute values.
func (m *Manager) reportRefused(kb string, names []string) {
	key := "abs:" + kb
	msg := strings.Join(names, ", ")
	m.mu.Lock()
	if m.varsErr == nil {
		m.varsErr = map[string]string{}
	}
	prev := m.varsErr[key]
	m.varsErr[key] = msg
	m.mu.Unlock()
	if msg != "" && msg != prev {
		m.event(Event{Kind: "todos.vars_refused", Path: kb + "/" + varsFile,
			Detail: "absolute values are a machine's, not the KB's (set them with scope \"machine\"): " + msg})
	}
}

// relTo is target relative to dir, the form a KB's file carries to another machine; target as it is
// when no relative form exists.
func relTo(dir, target string) string {
	if rel, err := filepath.Rel(dir, target); err == nil && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	return target
}

// recordProjectRoot writes the project a claim names (AutoVim's legacy $WORKSPACE) into the store's
// workspace's file as PROJECT_ROOT, relative to the KB root, unless the file already names one. A
// "project" that is the KB root or inside it is a session started in the KB, not the project the
// KB serves: nothing is recorded.
func (m *Manager) recordProjectRoot(ctx context.Context, w where, project string) error {
	kb, err := m.owner(ctx, w.rec.ID)
	if err != nil || kb == "" || paths.Under(path.Clean(project), kb) {
		return err
	}
	m.varsMu.Lock()
	defer m.varsMu.Unlock()
	defer m.unfold(kb)
	return m.editKBFile(ctx, kb, func(sv *storeVars) error {
		if sv.Vars[VarProjectRoot] == "" && !slices.Contains(sv.Machine, VarProjectRoot) {
			sv.Vars[VarProjectRoot] = relTo(kb, project)
		}
		return nil
	})
}

// THE MIGRATION (docs/decisions/todos.md#migrating-store-variable-files)

// unfold makes the next read of kb's variables fold its stores' files again.
func (m *Manager) unfold(kb string) {
	m.mu.Lock()
	delete(m.folded, kb)
	m.mu.Unlock()
}

// fold carries the variables of kb's stores (each store's old file, and this machine's old per-store
// values) into kb's: a relative value, rebased to the KB root, into the KB's file; an absolute value
// into this machine's row, its name declared machine-only in the KB's file. Only when every value has
// a destination is a store's file renamed. A name two sources set differently is a conflict: nothing
// is carried, every file stays, and the conflicting names are returned, unresolved below this
// machine's row until a KB-file value or a machine-only declaration settles them.
func (m *Manager) fold(ctx context.Context, kb string) map[string]bool {
	m.mu.Lock()
	done := m.folded[kb]
	m.mu.Unlock()
	if done {
		return nil
	}
	// one fold or set_var at a time: a fold's KB-file write and rows never interleave with a verb's
	m.varsMu.Lock()
	defer m.varsMu.Unlock()
	st, err := m.j.Settings(ctx)
	if err != nil {
		return nil
	}
	type source struct {
		id, dir string
		file    storeVars
		hasFile bool
		machine map[string]string
	}
	var srcs []source
	blocked := false
	for _, id := range slices.Sorted(maps.Keys(st.Stores)) {
		rec := st.Stores[id]
		if rec.KB != kb || m.ownerOf(rec) != kb {
			continue // another workspace's, or recorded to a removed one: folded when that one is back
		}
		sv, ok, err := m.readVarsFile(ctx, rec.Path)
		if err != nil {
			m.reportVarsFile(id, rec.Path, err)
			blocked = true // a refused store file is mended by hand first: nothing of it is lost
			continue
		}
		if ok || len(st.MachineVars[id]) > 0 {
			srcs = append(srcs, source{id: id, dir: rec.Path, file: sv, hasFile: ok, machine: st.MachineVars[id]})
		}
	}
	if len(srcs) == 0 {
		if !blocked {
			m.mu.Lock()
			if m.folded == nil {
				m.folded = map[string]bool{}
			}
			m.folded[kb] = true
			m.mu.Unlock()
		}
		return nil
	}
	kbFile, _, err := m.readVarsFile(ctx, kb)
	if err != nil {
		m.reportVarsFile("kb:"+kb, kb, err)
		return nil // the KB's file is mended first; the stores' files wait
	}
	settled := func(n string) bool { _, ok := kbFile.Vars[n]; return ok || slices.Contains(kbFile.Machine, n) }
	portable := map[string]map[string]bool{} // a name's values for the KB's file, relative to kb
	rows := map[string]map[string]bool{}     // a name's values for this machine's row
	machineOnly := map[string]bool{}
	add := func(to map[string]map[string]bool, n, v string) {
		if to[n] == nil {
			to[n] = map[string]bool{}
		}
		to[n][v] = true
	}
	for _, s := range srcs {
		for n, v := range s.file.Vars {
			switch {
			case strings.HasPrefix(v, "$"):
				add(portable, n, v) // a value under another variable stays as written
			case isAbsValue(v):
				add(rows, n, m.absValue(kb, v))
				machineOnly[n] = true
			default:
				add(portable, n, relTo(kb, path.Join(s.dir, v)))
			}
		}
		for _, n := range s.file.Machine {
			machineOnly[n] = true
		}
		for n, v := range s.machine {
			add(rows, n, v)
		}
	}
	conflict := map[string]bool{}
	for n, vals := range portable {
		if !settled(n) && (len(vals) > 1 || machineOnly[n]) {
			conflict[n] = true
		}
	}
	for n, vals := range rows {
		if st.WorkspaceVars[kb][n] == "" && len(vals) > 1 {
			conflict[n] = true
		}
	}
	if len(conflict) > 0 {
		key := "conflict:" + kb
		msg := strings.Join(slices.Sorted(maps.Keys(conflict)), ", ")
		m.mu.Lock()
		if m.varsErr == nil {
			m.varsErr = map[string]string{}
		}
		prev := m.varsErr[key]
		m.varsErr[key] = msg
		m.mu.Unlock()
		if msg != prev {
			m.event(Event{Kind: "todos.vars_conflict", Path: kb + "/" + varsFile, Detail: "stores of this workspace set " + msg +
				" differently: set each in the KB's file (scope kb) or declare it machine-only (scope machine, machine_only)"})
		}
		return conflict
	}
	err = m.editKBFile(ctx, kb, func(sv *storeVars) error {
		for n, vals := range portable {
			if !settled(n) {
				for v := range vals {
					sv.Vars[n] = v
				}
			}
		}
		for n := range machineOnly {
			if !settled(n) && sv.Vars[n] == "" && !slices.Contains(sv.Machine, n) {
				sv.Machine = append(sv.Machine, n)
			}
		}
		return nil
	})
	if err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_migrate.failed", "kb": kb})
		return nil
	}
	err = m.j.UpdateSettings(ctx, func(s *Settings) error {
		if s.WorkspaceVars == nil {
			s.WorkspaceVars = map[string]map[string]string{}
		}
		vals := maps.Clone(s.WorkspaceVars[kb])
		if vals == nil {
			vals = map[string]string{}
		}
		for n, vs := range rows {
			if vals[n] == "" {
				for v := range vs {
					vals[n] = v
				}
			}
		}
		if len(vals) > 0 {
			s.WorkspaceVars[kb] = vals
		}
		for _, src := range srcs {
			delete(s.MachineVars, src.id)
		}
		return nil
	})
	if err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_migrate.failed", "kb": kb})
		return nil
	}
	for _, s := range srcs {
		if !s.hasFile {
			continue
		}
		if err := m.retire(ctx, s.dir); err != nil {
			logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_migrate.failed", "store": s.id})
			return nil // the next read folds again: the carried values are settled now, so only the rename is left
		}
		m.event(Event{Kind: "todos.vars_migrated", Store: s.id, Path: s.dir + "/" + varsFile,
			Detail: "carried into " + kb + "/" + varsFile + " and this machine's rows; renamed " + varsFile + migratedSuffix})
	}
	m.storesChanged()
	return nil
}

// retire renames a store's carried variable file, so it is never read again.
func (m *Manager) retire(ctx context.Context, dir string) error {
	fsys, _, err := m.disk.Open(ctx, dir)
	if err != nil {
		return err
	}
	defer fsys.Close()
	return fsys.Rename(ctx, varsFile, varsFile+migratedSuffix)
}

// MigrateVars is docs/decisions/todos.md#migrating-store-variable-files, run once as the daemon
// starts serving: every store's workspace is recorded (the containing one; for a store outside
// every workspace, the workspace an older route's kb_root named), the routes' kb_roots are cleared,
// and each workspace's stores are folded in.
func (m *Manager) MigrateVars(ctx context.Context) {
	st, err := m.j.Settings(ctx)
	if err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_migrate.failed"})
		return
	}
	hint := map[string]string{} // a store's path: the workspace root an older route's kb_root named
	for _, root := range slices.Sorted(maps.Keys(st.Roots)) {
		rr := st.Roots[root]
		if rr.KBRoot == "" {
			continue
		}
		sp := rr.Override
		if sp == "" {
			sp = root + "/" + todoDir
		}
		if m.nameOf(path.Clean(rr.KBRoot)) != "" {
			hint[sp] = path.Clean(rr.KBRoot)
		}
	}
	kbs := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(st.Stores)) {
		if ctx.Err() != nil {
			return
		}
		rec := st.Stores[id]
		kb, err := m.owner(ctx, id)
		if err != nil {
			logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_migrate.failed", "store": id})
			continue
		}
		if kb == "" && hint[rec.Path] != "" {
			kb = hint[rec.Path]
			err := m.j.UpdateSettings(ctx, func(s *Settings) error {
				if r, ok := s.Stores[id]; ok && r.KB == "" {
					r.KB = kb
					s.Stores[id] = r
				}
				return nil
			})
			if err != nil {
				continue
			}
		}
		if kb != "" {
			kbs[kb] = true
		}
	}
	err = m.j.UpdateSettings(ctx, func(s *Settings) error {
		for root, rr := range s.Roots {
			if rr.KBRoot != "" {
				rr.KBRoot = ""
				s.Roots[root] = rr
			}
		}
		return nil
	})
	if err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_migrate.failed"})
	}
	for _, kb := range slices.Sorted(maps.Keys(kbs)) {
		m.fold(ctx, kb)
	}
}
