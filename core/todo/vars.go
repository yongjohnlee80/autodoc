package todo

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/paths"
)

// Builtin variable names. They are derived from the store's location (storevars.go); a store's
// variable file or this machine may override them, a global user var may not.
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

func isBuiltin(name string) bool {
	return name == VarKBRoot || name == VarWorkspace || name == VarProjectRoot || name == VarHome
}

// scope is what a store's refs resolve against, the same from every route to the store: its
// built-ins and its other variables, each the first of this machine's value, the store's file, the
// derived value and a global user var (Manager.scope), then the environment.
type scope struct {
	kbRoot, workspace, projectRoot, home string
	user                                 map[string]string // the effective variables that are not built in
	machine                              map[string]bool   // the names the store's file declares per machine
	env                                  func(string) string
	exists                               func(string) bool
}

func (s scope) get(name string) string {
	switch name {
	case VarKBRoot:
		return s.kbRoot
	case VarWorkspace:
		return s.workspace
	case VarProjectRoot:
		return s.projectRoot
	case VarHome:
		return s.home
	}
	if v := s.user[name]; v != "" {
		return v
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
			return resolved{path: val, varName: name}
		}
		return resolved{path: trailSlashe.ReplaceAllString(val, "") + "/" + leadSlashes.ReplaceAllString(rest, ""), varName: name}
	}
	if strings.HasPrefix(input, "/") || strings.HasPrefix(input, "~") {
		return resolved{path: s.expandHome(input)}
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
		if value = trailSlashe.ReplaceAllString(value, ""); value != "" {
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

	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		abs := s.expandHome(p)
		var best *root
		for i, r := range roots {
			if paths.Under(abs, r.value) {
				if best == nil || len(r.value) > len(best.value) {
					best = &roots[i]
				}
			}
		}
		if best == nil {
			return p
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
	case strings.HasPrefix(b, "/"):
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
			if r.unresolved {
				add(field, CodeUnresolved, "variable '$"+r.varName+"' is not defined on this machine — "+
					"set it in the Vars section or as an environment variable")
				continue
			}
			abs := ""
			switch {
			case strings.HasPrefix(r.path, "/"):
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
