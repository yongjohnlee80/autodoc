package todo

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/paths"
)

// Builtin variable names: $KB_ROOT is the workspace's root and $HOME the daemon user's home
// (storevars.go). A KB's file or this machine may override them, a global user var may not.
// VarWorkspace is AutoVim's retired $WORKSPACE (docs/decisions/todos.md#retiring-workspace): an
// ordinary name now, unset unless a person sets it, which todos.migrate_refs rewrites to
// $PROJECT_ROOT.
const (
	VarKBRoot    = "KB_ROOT"
	VarWorkspace = "WORKSPACE"
	VarHome      = "HOME"
)

var (
	varNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	braceVarRe  = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}(.*)$`)
	plainVarRe  = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)(.*)$`)
	trailSlashe = regexp.MustCompile(`/+$`)
	leadSlashes = regexp.MustCompile(`^/+`)
)

func isBuiltin(name string) bool { return name == VarKBRoot || name == VarHome }

// scope is what a store's refs resolve against, the same from every route to the store: its
// workspace's built-ins and other variables, each the first of this machine's value, the KB's file,
// the derived value and a global user var (Manager.scope), then the environment. A name a pending
// migration holds in conflict has only this machine's value: never the environment's.
type scope struct {
	kbRoot, workspace, projectRoot, home string
	// storeHome is the store's parent: what the daemon derived $WORKSPACE as before it was retired
	// (docs/decisions/todos.md#retiring-workspace), read only to tell where a Lua-era $WORKSPACE
	// ref exists
	storeHome string
	user      map[string]string // the effective variables that are not built in
	machine   map[string]bool   // the names the KB's file declares per machine
	conflict  map[string]bool   // the names a pending migration holds in conflict
	env       func(string) string
	exists    func(string) bool
	// read and list are a small file's content and a directory's entry names, by absolute path;
	// withExists gives them with exists. ok is false for what cannot be read.
	read func(string) ([]byte, bool)
	list func(string) []string
	// windows is the path rule refs are read under: this OS's (paths.Windows), Windows' in a test
	windows bool
}

// osAbs is an absolute path given as a ref, a variable's value or what ~ or a variable expanded
// to, in the slash form every other path in core/todo has: under Windows' rule `C:\kb\a.md` is
// "C:/kb/a.md" and `\\server\share\a.md` "//server/share/a.md". Under Unix' rule it is as written: a
// backslash is a name's own character there. A path that is not absolute is as written too.
func (s scope) osAbs(p string) string {
	if !s.windows || !paths.IsAbsFor(p, true) {
		return p
	}
	return paths.SlashFor(p, true)
}

// isAbs reports whether p is absolute under the scope's rule.
func (s scope) isAbs(p string) bool { return paths.IsAbsFor(p, s.windows) }

// get is name's value, as layers.value answers it: $WORKSPACE and $PROJECT_ROOT are ordinary names,
// in user when set.
func (s scope) get(name string) string {
	switch name {
	case VarKBRoot:
		return s.kbRoot
	case VarHome:
		return s.home
	}
	if v := s.user[name]; v != "" {
		return v
	}
	if s.conflict[name] {
		return "" // unresolved below this machine's row until the conflict is settled
	}
	if s.env != nil {
		return s.env(name)
	}
	return ""
}

// expandHome expands a leading ~ as vim.fn.expand does for these refs.
func (s scope) expandHome(p string) string {
	if s.home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		return s.home + p[1:]
	}
	return p
}

// resolved is vars.lua's resolve_path: a $VAR or ${VAR} prefix substituted, ~ expanded, a plain
// relative path returned for the caller to resolve.
type resolved struct {
	path       string
	varName    string
	unresolved bool
}

func (s scope) resolve(input string) resolved {
	name, rest := splitVar(input)
	if name != "" {
		val := s.get(name)
		if val == "" {
			return resolved{path: input, varName: name, unresolved: true}
		}
		if rest == "" {
			return resolved{path: s.osAbs(val), varName: name}
		}
		return resolved{path: s.osAbs(trailSlashe.ReplaceAllString(val, "") + "/" + leadSlashes.ReplaceAllString(rest, "")), varName: name}
	}
	if s.isAbs(input) || strings.HasPrefix(input, "~") {
		return resolved{path: s.osAbs(s.expandHome(input))}
	}
	return resolved{path: input}
}

func splitVar(input string) (name, rest string) {
	if !strings.HasPrefix(input, "$") {
		return "", ""
	}
	if m := braceVarRe.FindStringSubmatch(input); m != nil {
		return m[1], m[2]
	}
	if m := plainVarRe.FindStringSubmatch(input); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

// symbolize is vars.lua's symbolize_path: an absolute (or ~) path is written against the root
// with the longest matching prefix, KB_ROOT, WORKSPACE, PROJECT_ROOT, the user's vars and HOME, the
// earlier one winning a tie. A bare relative path is the KB's, so it becomes $KB_ROOT/… when the KB holds it.
func (s scope) symbolize(p string) string {
	if p == "" || strings.HasPrefix(p, "$") {
		return p
	}
	type root struct{ name, value string }
	var roots []root
	add := func(name, value string) {
		if value = trailSlashe.ReplaceAllString(s.osAbs(value), ""); value != "" {
			roots = append(roots, root{name, value})
		}
	}
	add(VarKBRoot, s.kbRoot)
	add(VarWorkspace, s.workspace)
	add(VarProjectRoot, s.projectRoot)
	names := make([]string, 0, len(s.user))
	for k := range s.user {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		add(k, s.user[k])
	}
	add(VarHome, s.home)

	if s.isAbs(p) || strings.HasPrefix(p, "~") {
		abs := s.osAbs(s.expandHome(p))
		var best *root
		for i, r := range roots {
			// compared as the scope's OS compares paths: on Windows without regard to case. A name
			// written in its 8.3 short form (RUNNER~1) is another spelling, not compared as its long
			// form: core/todo reads no disk to resolve it, so such a ref is kept as written.
			if paths.UnderAbsFor(abs, r.value, s.windows) {
				if best == nil || len(r.value) > len(best.value) {
					best = &roots[i]
				}
			}
		}
		if best == nil && strings.HasPrefix(p, "~") {
			return p // ~ is itself portable
		}
		if best == nil {
			return abs // an absolute path in the slash form: what every machine's reader compares
		}
		return "$" + best.name + abs[len(best.value):]
	}
	if s.kbRoot != "" && s.exists != nil && s.exists(joinPath(trailSlashe.ReplaceAllString(s.kbRoot, ""), p)) {
		return "$" + VarKBRoot + "/" + leadSlashes.ReplaceAllString(p, "")
	}
	return p
}

// joinPath is fs/path.lua's join of two parts: an absolute second part replaces the first.
func joinPath(a, b string) string {
	switch {
	case b == "":
		return a
	case paths.IsAbs(b):
		return b
	case strings.HasSuffix(a, "/"):
		return a + b
	}
	return a + "/" + b
}

// symbolizeRefs rewrites t's adr and review refs to their portable form and reports a change.
func (s scope) symbolizeRefs(t *Task) bool {
	changed := false
	for _, list := range [][]string{t.ADR, t.Review} {
		for i, p := range list {
			if np := s.symbolize(p); np != p {
				list[i] = np
				changed = true
			}
		}
	}
	return changed
}

// refErrors is auto-core's compute_errors: adr and review refs that do not resolve or do not
// exist, and blocked ids no bucket holds. An error that persists keeps its detected time.
func (s scope) refErrors(t Task, now string, hasTask func(id string) bool) []ErrorEntry {
	prior := map[string]string{}
	for _, e := range t.Errors {
		prior[e.Field+"|"+e.Code] = e.Detected
	}
	var out []ErrorEntry
	add := func(field, code, msg string) {
		detected := prior[field+"|"+code]
		if detected == "" {
			detected = now
		}
		out = append(out, ErrorEntry{Field: field, Code: code, Message: msg, Detected: detected})
	}
	refs := func(name string, list []string) {
		for i, p := range list {
			if p == "" {
				continue
			}
			field := fmt.Sprintf("%s[%d]", name, i)
			r := s.resolve(p)
			if r.unresolved && s.machine[r.varName] {
				continue // set on some machines only: a fact about this machine, not about the task
			}
			if r.unresolved && s.conflict[r.varName] {
				add(field, CodeUnresolved, "variable '$"+r.varName+"' is held in conflict: this workspace's stores set it "+
					"differently; set it in the KB's file, or declare it machine-only (workspace.set_var)")
				continue
			}
			if r.unresolved {
				add(field, CodeUnresolved, "variable '$"+r.varName+"' is not defined on this machine — "+
					"set it in the Vars section or as an environment variable")
				continue
			}
			abs := ""
			switch {
			case s.isAbs(r.path):
				abs = r.path
			case s.kbRoot != "":
				abs = joinPath(s.kbRoot, p) // a relative ref is the KB's, and only the KB's
			}
			if abs != "" && (s.exists == nil || !s.exists(abs)) {
				add(field, CodeNotFound, "path '"+p+"' does not exist (resolved: "+abs+")")
			}
		}
	}
	refs("adr", t.ADR)
	refs("review", t.Review)
	for i, id := range t.Blocked {
		if id != "" && !hasTask(id) {
			add(fmt.Sprintf("blocked[%d]", i), CodeNotFound, "Task '"+id+"' does not exist in this workspace")
		}
	}
	return out
}

// sameErrors reports two errors[] lists equal entry by entry, as refresh compares them.
func sameErrors(a, b []ErrorEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
