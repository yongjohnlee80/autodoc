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
)

// A STORE'S VARIABLES (ADR 1792015069). A store's refs resolve against one scope, the store's,
// whichever route (a workspace name, a folder root) reached it, so every client reads and writes
// the same refs. The scope is the store's derived built-ins, overridden by its variable file
// (<store>/.autodoc-vars.toml, which travels with the KB) and by this machine's per-store values.

// varsFile is a store's variable file, beside its buckets.
const varsFile = ".autodoc-vars.toml"

// maxVarsFile is the largest variable file read: a larger one is refused whole, never cut short.
const maxVarsFile = 64 << 10

// VarProjectRoot is the project a store serves: what auto-core's $WORKSPACE meant before the cutover.
const VarProjectRoot = "PROJECT_ROOT"

// kbMarker is the file whose frontmatter makes a directory a KB: its AGENTS.md with "type: kb". A
// project's own AGENTS.md has no such frontmatter, so it never makes the project a KB.
const kbMarker = "AGENTS.md"

// storeVars is a store's variable file as read: its variables, by name, as written (a relative path
// is the store directory's), and the names whose value is set per machine.
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
// a variable name, or a name both machine-only and given a value here is refused whole: the store
// then resolves as if the file were absent.
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

// render writes sv as the file's TOML: the machine list, then [vars], each sorted, so the file a
// verb writes is stable and diffs cleanly.
func (sv storeVars) render() []byte {
	var b strings.Builder
	b.WriteString("# AutoDoc todo variables for this store (ADR 1792015069). A relative path is this\n")
	b.WriteString("# directory's. machine: the names whose value is set per machine, never here.\n")
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
		names := make([]string, 0, len(sv.Vars))
		for k := range sv.Vars {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			fmt.Fprintf(&b, "%s = %q\n", k, sv.Vars[k])
		}
	}
	return []byte(b.String())
}

// abs is a file value as a path: a relative one is the store directory's; an absolute one, a ~ one
// or one starting with a variable stays as written.
func (sv storeVars) abs(dir, v string) string {
	if v == "" || strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~") || strings.HasPrefix(v, "$") {
		return v
	}
	return path.Join(dir, v)
}

// readStoreVars reads the variable file of the store at dir. An absent file, or an absent store, is
// no variables; a file that cannot be read or parsed is its error, and the store resolves without it.
func (m *Manager) readStoreVars(ctx context.Context, dir string) (storeVars, error) {
	fsys, _, err := m.disk.Open(ctx, dir)
	if err != nil {
		if errors.Is(err, vfs.ErrNotExist) {
			return storeVars{}, nil
		}
		return storeVars{}, err
	}
	defer fsys.Close()
	r, err := fsys.Open(ctx, varsFile, 0)
	if err != nil {
		if errors.Is(err, vfs.ErrNotExist) {
			return storeVars{}, nil
		}
		return storeVars{}, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, maxVarsFile+1))
	if err != nil {
		return storeVars{}, err
	}
	if len(b) > maxVarsFile {
		return storeVars{}, fmt.Errorf("%s/%s: larger than %d bytes", dir, varsFile, maxVarsFile)
	}
	sv, err := parseStoreVars(b)
	if err != nil {
		return storeVars{}, fmt.Errorf("%s/%s: %w", dir, varsFile, err)
	}
	return sv, nil
}

// kbOf is the KB a store sits in: the nearest directory at or above dir whose AGENTS.md declares
// "type: kb" in its frontmatter; "" for none.
func (m *Manager) kbOf(ctx context.Context, dir string) string {
	for d := path.Clean(dir); ; d = path.Dir(d) {
		if m.isKB(ctx, d) {
			return d
		}
		if d == "/" || d == "." {
			return ""
		}
	}
}

// isKB reports whether dir's AGENTS.md opens with frontmatter that holds "type: kb".
func (m *Manager) isKB(ctx context.Context, dir string) bool {
	fsys, _, err := m.disk.Open(ctx, dir)
	if err != nil {
		return false
	}
	defer fsys.Close()
	r, err := fsys.Open(ctx, kbMarker, 0)
	if err != nil {
		return false
	}
	defer r.Close()
	b, _ := io.ReadAll(io.LimitReader(r, 4<<10))
	return kbFrontmatter(b)
}

// kbFrontmatter reports frontmatter (a first line "---", up to the next "---") with a line
// "type: kb", quoted or not.
func kbFrontmatter(b []byte) bool {
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return false
	}
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		if l == "---" {
			return false
		}
		if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "type" &&
			strings.Trim(strings.TrimSpace(v), `"'`) == "kb" {
			return true
		}
	}
	return false
}

// layers are a store's variables before precedence: this machine's values, the store's file, the
// derived built-ins and the global user vars.
type layers struct {
	dir     string            // the store's directory
	machine map[string]string // this machine's values for the store
	file    storeVars         // the store's file; empty when absent or refused
	fileErr error             // why the file was refused; nil when read or absent
	derived map[string]string // the built-ins from the store's location
	global  map[string]string
}

// layers reads w's store's variables. A refused file is reported once per distinct error, and the
// store resolves without it.
func (m *Manager) layers(ctx context.Context, w where) (layers, error) {
	st, err := m.j.Settings(ctx)
	if err != nil {
		return layers{}, err
	}
	dir := w.rec.Path
	if rec, ok := st.Stores[w.rec.ID]; ok {
		dir = rec.Path
	}
	home := path.Dir(dir) // the store's home: its parent, by location alone (ADR 1792015069 §2.1)
	l := layers{dir: dir, machine: st.MachineVars[w.rec.ID], global: st.Vars,
		derived: map[string]string{VarKBRoot: m.kbOf(ctx, home), VarWorkspace: home, VarProjectRoot: "", VarHome: m.o.home}}
	l.file, l.fileErr = m.readStoreVars(ctx, dir)
	m.reportVarsFile(w.rec.ID, dir, l.fileErr)
	return l, nil
}

// value is name's effective value and where it came from: this machine, the store's file, the
// derived built-in, a global user var (never for a built-in), then the environment.
func (l layers) value(name string, env func(string) string) (string, string) {
	if v := l.machine[name]; v != "" {
		return v, "machine"
	}
	if v := l.file.Vars[name]; v != "" {
		return l.file.abs(l.dir, v), "store"
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

// names are every variable the layers name, built-ins first, then the rest sorted.
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
	for n := range l.global {
		add(n)
	}
	sort.Strings(rest)
	return append([]string{VarKBRoot, VarWorkspace, VarProjectRoot, VarHome}, rest...)
}

// scope is the store's scope: what every route to the store resolves refs against.
func (m *Manager) scope(ctx context.Context, w where) (scope, error) {
	l, err := m.layers(ctx, w)
	if err != nil {
		return scope{}, err
	}
	get := func(n string) string { v, _ := l.value(n, nil); return v }
	sc := scope{kbRoot: get(VarKBRoot), workspace: get(VarWorkspace), projectRoot: get(VarProjectRoot), home: get(VarHome),
		user: map[string]string{}, machine: map[string]bool{}, env: m.o.env}
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
	return sc, nil
}

// Vars lists the variables an At's store's refs may use, the same from every route: the built-ins
// with their derived values, then the rest, each with its effective value and its source.
func (m *Manager) Vars(ctx context.Context, at At) ([]Var, error) {
	w, err := m.resolve(ctx, at)
	if err != nil {
		return nil, err
	}
	l, err := m.layers(ctx, w)
	if err != nil {
		return nil, err
	}
	machine := map[string]bool{}
	for _, n := range l.file.Machine {
		machine[n] = true
	}
	var out []Var
	for _, n := range l.names() {
		v, src := l.value(n, m.o.env)
		out = append(out, Var{Name: n, Value: v, Source: src, Builtin: isBuiltin(n), Derived: l.derived[n], Machine: machine[n]})
	}
	return out, nil
}

// Var scopes: where todos.set_var keeps a value.
const (
	ScopeGlobal  = "global"  // the daemon's user vars, for every store; never a built-in
	ScopeStore   = "store"   // the store's file: travels with the KB
	ScopeMachine = "machine" // this machine's value for the store; the name is declared machine-only in the file
)

// SetVarOpts says where SetVar keeps a value: an At's store, in its file or for this machine, or
// (no At) the global user vars.
type SetVarOpts struct {
	At    *At    `json:"at,omitempty"`
	Scope string `json:"scope,omitempty"` // "global" (the default without an At), "store" (with one), or "machine"
}

// SetVar sets a variable, or clears it with "". A global var is any name but a built-in's; a store
// or machine value may override a built-in.
func (m *Manager) SetVar(ctx context.Context, name, value string, opts ...SetVarOpts) error {
	if !varNameRe.MatchString(name) {
		return fmt.Errorf("%w: %q is not a variable name (letters, digits and _, not starting with a digit)", ErrInvalid, name)
	}
	var o SetVarOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	scope := o.Scope
	if scope == "" {
		scope = ScopeGlobal
		if o.At != nil {
			scope = ScopeStore
		}
	}
	switch {
	case scope == ScopeGlobal:
		if isBuiltin(name) {
			return fmt.Errorf("%w: %s is derived per store; override it for a store, not globally", ErrInvalid, name)
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
	case o.At == nil:
		return fmt.Errorf("%w: scope %q needs an at", ErrInvalid, scope)
	case scope == ScopeStore:
		w, err := m.writable(ctx, *o.At)
		if err != nil {
			return err
		}
		return m.editStoreVars(ctx, w, func(sv *storeVars) error {
			if slices.Contains(sv.Machine, name) && value != "" {
				return fmt.Errorf("%w: %s is machine-only in this store; set it with scope \"machine\"", ErrInvalid, name)
			}
			if value == "" {
				delete(sv.Vars, name)
			} else {
				sv.Vars[name] = value
			}
			return nil
		})
	case scope == ScopeMachine:
		w, err := m.writable(ctx, *o.At)
		if err != nil {
			return err
		}
		if value != "" {
			// the name is declared machine-only in the store's file first, so no machine without
			// it errs; a file edit that fails leaves no value behind
			err := m.editStoreVars(ctx, w, func(sv *storeVars) error {
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
		return m.j.UpdateSettings(ctx, func(s *Settings) error {
			if s.MachineVars == nil {
				s.MachineVars = map[string]map[string]string{}
			}
			vals := maps.Clone(s.MachineVars[w.rec.ID])
			if vals == nil {
				vals = map[string]string{}
			}
			if value == "" {
				delete(vals, name)
			} else {
				vals[name] = value
			}
			if len(vals) == 0 {
				delete(s.MachineVars, w.rec.ID)
			} else {
				s.MachineVars[w.rec.ID] = vals
			}
			return nil
		})
	}
	return fmt.Errorf("%w: scope %q: global, store or machine", ErrInvalid, scope)
}

// editStoreVars changes w's store's variable file through fn, written atomically at the version it
// was read at; a file left with nothing is removed. A refused file is never rewritten.
func (m *Manager) editStoreVars(ctx context.Context, w where, fn func(*storeVars) error) error {
	st, err := m.open(ctx, w)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	var ver vfs.Version
	sv := storeVars{Vars: map[string]string{}}
	doc, err := st.docs.Read(ctx, varsFile)
	switch {
	case errors.Is(err, vfs.ErrNotExist):
	case err != nil:
		return err
	default:
		ver = doc.Version
		if sv, err = parseStoreVars(doc.Content); err != nil {
			return fmt.Errorf("%w: %s/%s is not a variable file the daemon rewrites (fix it by hand): %v", ErrInvalid, st.path, varsFile, err)
		}
	}
	if err := fn(&sv); err != nil {
		return err
	}
	if len(sv.Vars) == 0 && len(sv.Machine) == 0 {
		if ver == "" {
			return nil
		}
		return st.docs.Remove(ctx, varsFile, ver)
	}
	_, err = st.docs.Write(ctx, varsFile, sv.render(), ver)
	if err == nil {
		m.event(Event{Kind: "todos.vars", Store: st.id, Path: st.path + "/" + varsFile})
	}
	return err
}

// reportVarsFile reports a store's refused variable file once per distinct error, and its recovery.
func (m *Manager) reportVarsFile(id, dir string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.mu.Lock()
	if m.varsErr == nil {
		m.varsErr = map[string]string{}
	}
	prev, seen := m.varsErr[id]
	m.varsErr[id] = msg
	m.mu.Unlock()
	if msg != "" && msg != prev {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_invalid", "store": id, "path": dir + "/" + varsFile})
		m.event(Event{Kind: "todos.vars_invalid", Store: id, Path: dir + "/" + varsFile, Detail: msg})
	} else if msg == "" && seen && prev != "" {
		m.event(Event{Kind: "todos.vars_valid", Store: id, Path: dir + "/" + varsFile})
	}
}

// relTo is target written relative to dir when it is under dir's parent chain on the same tree (a
// path the file can carry to another machine); otherwise target as it is.
func relTo(dir, target string) string {
	if rel, err := filepath.Rel(dir, target); err == nil && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	return target
}

// recordProjectRoot writes the project a claim names (auto-core's legacy $WORKSPACE) into the
// store's file as PROJECT_ROOT, relative to the store, unless the file already names one.
func (m *Manager) recordProjectRoot(ctx context.Context, w where, project string) error {
	return m.editStoreVars(ctx, w, func(sv *storeVars) error {
		if sv.Vars[VarProjectRoot] == "" && !slices.Contains(sv.Machine, VarProjectRoot) {
			sv.Vars[VarProjectRoot] = relTo(w.rec.Path, project)
		}
		return nil
	})
}

// CarryKBRoots is ADR 1792015069 §2.7 step 1, run once as the daemon starts serving: the kb_root
// older clients recorded on a claimed store's routes moves into the store's file when the routes
// agree on it and the store's location derives something else (or nothing); then the routes' values
// are cleared, so a route never carries one again. Routes that disagree are reported, and nothing of
// that store is written or cleared: the user decides.
func (m *Manager) CarryKBRoots(ctx context.Context) {
	st, err := m.j.Settings(ctx)
	if err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_carry.failed"})
		return
	}
	byStore := map[string][]string{} // a store's path: the roots that resolve to it with a kb_root
	for _, root := range slices.Sorted(maps.Keys(st.Roots)) {
		rr := st.Roots[root]
		if rr.KBRoot == "" {
			continue
		}
		sp := rr.Override
		if sp == "" {
			sp = root + "/" + todoDir
		}
		byStore[sp] = append(byStore[sp], root)
	}
	for _, id := range slices.Sorted(maps.Keys(st.Stores)) {
		rec := st.Stores[id]
		roots := byStore[rec.Path]
		if len(roots) == 0 || !rec.Claimed || ctx.Err() != nil {
			continue
		}
		vals := map[string]bool{}
		for _, r := range roots {
			vals[st.Roots[r].KBRoot] = true
		}
		if len(vals) > 1 {
			m.event(Event{Kind: "todos.vars_carry_conflict", Store: id, Path: rec.Path,
				Detail: "routes " + strings.Join(roots, ", ") + " name different kb_roots: " + strings.Join(slices.Sorted(maps.Keys(vals)), ", ")})
			continue
		}
		kb := st.Roots[roots[0]].KBRoot
		w, err := m.whereOf(ctx, st, rec)
		if err != nil {
			logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_carry.failed", "store": id})
			continue
		}
		l, err := m.layers(ctx, w)
		if err != nil {
			continue
		}
		switch {
		case l.fileErr != nil:
			// the file cannot take it: the routes keep their kb_root until the file is mended, and
			// the next start carries it
			m.event(Event{Kind: "todos.vars_carry_blocked", Store: id, Path: rec.Path,
				Detail: "kb_root " + kb + " kept on the routes: " + l.fileErr.Error()})
			continue
		case l.file.Vars[VarKBRoot] != "" || kb == l.derived[VarKBRoot]:
			// the store's file already names a KB_ROOT, or its place derives this one
		default:
			err := m.editStoreVars(ctx, w, func(sv *storeVars) error {
				sv.Vars[VarKBRoot] = relTo(rec.Path, kb)
				return nil
			})
			if err != nil {
				logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_carry.failed", "store": id})
				continue
			}
			m.event(Event{Kind: "todos.vars_carried", Store: id, Path: rec.Path, Detail: "KB_ROOT = " + kb})
		}
		err = m.j.UpdateSettings(ctx, func(s *Settings) error {
			for _, r := range roots {
				rr := s.Roots[r]
				rr.KBRoot = ""
				setRoot(s, rr)
			}
			return nil
		})
		if err != nil {
			logger.Warning(m.o.log, err, logger.Fields{"event": "todos.vars_carry.failed", "store": id})
		}
	}
}
