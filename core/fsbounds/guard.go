package fsbounds

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/text/unicode/norm"
)

// Root is a served workspace: its name and its root folder (absolute).
type Root struct {
	Name, Dir string
}

// Guard is the daemon's half of the bounds: its own folders and files a script never reaches, and
// the workspaces it serves, for every bounded check. A nil Guard refuses every bounded operation.
//
// Concurrency: safe for concurrent use; SetServed may be called while checks run.
type Guard struct {
	protected []string // absolute: the store's folder (DATA), STATE, the daemon's config file and folder
	mu        sync.RWMutex
	served    func() []Root
}

// NewGuard is a guard that never lets a bounded call reach protected (absolute paths, made or not)
// or anything under them.
func NewGuard(protected ...string) *Guard {
	g := &Guard{}
	for _, p := range protected {
		if p != "" && filepath.IsAbs(p) {
			g.protected = append(g.protected, filepath.Clean(p))
		}
	}
	return g
}

// SetServed tells the guard the workspaces served now: the innermost root holding a path anchors
// its hidden rule, and a bounded file.* path is routed to it (Locate).
func (g *Guard) SetServed(served func() []Root) {
	g.mu.Lock()
	g.served = served
	g.mu.Unlock()
}

func (g *Guard) roots() []Root {
	g.mu.RLock()
	f := g.served
	g.mu.RUnlock()
	if f == nil {
		return nil
	}
	return f()
}

// allowed are the hidden names a bounded call reaches: AutoDoc's own workspace files, as the first
// component below a root, spelled exactly so.
var allowed = map[string]bool{".autodoc": true, ".todo-list": true, ".autodoc-vars.toml": true}

// op is what a check is for.
type op struct {
	write bool // the name is created, changed or removed
	tree  bool // the operation acts on whatever lies below the name (a rename, a copy, a RemoveAll)
}

var (
	opRead  = op{}
	opWrite = op{write: true}
	opTree  = op{write: true, tree: true}
	opCopy  = op{tree: true} // a copy's source: read, but the whole tree below it
)

// check refuses name (ws-relative, slash-separated, as the vfs has it) of workspace ws for o under b,
// or answers nil: the rules of the package doc, in the ADR's order.
func (g *Guard) check(ws Root, name string, b Bounds, o op) error {
	shown := name
	switch {
	case g == nil:
		return refuse(shown, whyUnknown)
	case o.write && b.Within == AnyWorkspace:
		return refuse(shown, whyAny)
	case o.write && b.Within != ws.Name:
		return refuse(shown, whyOther)
	case !o.write && b.Within != AnyWorkspace && b.Within != ws.Name:
		return refuse(shown, whyOther)
	}
	realRoot, tail, err := candidate(filepath.Clean(ws.Dir))
	if err != nil || len(tail) > 0 {
		return refuse(shown, whyUnknown) // a root that does not resolve whole serves no bounded call
	}
	abs := filepath.Join(ws.Dir, filepath.FromSlash(name)) // name is a valid vfs path: no "..", no root
	existing, tail, err := candidate(abs)
	if err != nil {
		return withPath(err, shown)
	}
	full := filepath.Join(append([]string{existing}, tail...)...)
	if !within(realRoot, full) {
		return refuse(shown, whyOutside)
	}
	anchor := g.innermost(full, realRoot)
	if o.write && anchor.Name != "" && anchor.Name != ws.Name {
		return refuse(shown, whyOther) // a nested workspace's file is that workspace's
	}
	if hidden(anchor.real, full) {
		return refuse(shown, whyHidden)
	}
	if g.ownPath(existing, tail, b.Protect) {
		return refuse(shown, whyOwn)
	}
	if o.tree { // the entry itself, not followed: a link to a folder moves as the link
		if fi, err := os.Lstat(abs); err == nil && fi.IsDir() {
			return refuse(shown, whyFolder)
		}
	}
	if len(tail) == 0 {
		fi, err := os.Stat(full)
		if err != nil {
			return refuse(shown, whyUnknown)
		}
		if fi.Mode().IsRegular() && links(fi) > 1 {
			return refuse(shown, whyLinked)
		}
	}
	return nil
}

// Locate is the served workspace a bounded file.* path (absolute) is in, and its name there: the
// innermost served root that holds the complete candidate. ok is false outside every root.
func (g *Guard) Locate(abs string) (ws Root, name string, err error) {
	if g == nil || !filepath.IsAbs(abs) {
		return Root{}, "", refuse(abs, whyOutside)
	}
	existing, tail, err := candidate(filepath.Clean(abs))
	if err != nil {
		return Root{}, "", withPath(err, abs)
	}
	full := filepath.Join(append([]string{existing}, tail...)...)
	in := g.innermost(full, "")
	if in.Name == "" {
		return Root{}, "", refuse(abs, whyOutside)
	}
	rel, err := filepath.Rel(in.real, full)
	if err != nil || rel == "." {
		return Root{}, "", refuse(abs, whyOutside)
	}
	return Root{Name: in.Name, Dir: in.Dir}, filepath.ToSlash(rel), nil
}

// anchored is a served root with its real path.
type anchored struct {
	Root
	real string
}

// innermost is the served root holding full (real) with the longest real path; fallback, when no
// served root does, is a root of that real path with no name (a guard told of no workspaces).
func (g *Guard) innermost(full, fallback string) anchored {
	best := anchored{real: fallback}
	for _, r := range g.roots() {
		real, tail, err := candidate(filepath.Clean(r.Dir))
		if err != nil || len(tail) > 0 || !within(real, full) {
			continue
		}
		if best.Name == "" || len(real) > len(best.real) {
			best = anchored{Root: r, real: real}
		}
	}
	return best
}

// hidden reports whether full, below root, has a component starting with "." that is not one of
// AutoDoc's own as the first.
func hidden(root, full string) bool {
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == "." {
		return false
	}
	for i, c := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(c, ".") && !(i == 0 && allowed[c]) {
			return true
		}
	}
	return false
}

// ownPath reports whether the candidate (existing, resolved, then tail) is one of the guard's or
// the call's protected paths or under one: by identity (os.SameFile) for a protected path that
// exists, against the candidate's existing part and each of its ancestors; by name, case-folded and
// NFC-normalised, below the same existing folder, for one not made yet.
func (g *Guard) ownPath(existing string, tail []string, extra []string) bool {
	var ancestors []os.FileInfo
	for a := existing; ; a = filepath.Dir(a) {
		if fi, err := os.Stat(a); err == nil {
			ancestors = append(ancestors, fi)
		}
		if a == filepath.Dir(a) {
			break
		}
	}
	for _, p := range append(append([]string(nil), g.protected...), clean(extra)...) {
		pe, ptail, err := candidate(p)
		if err != nil {
			return true // a protected path that does not resolve: fail closed
		}
		pfi, err := os.Stat(pe)
		if err != nil {
			return true
		}
		if len(ptail) == 0 {
			for _, a := range ancestors {
				if os.SameFile(a, pfi) {
					return true
				}
			}
			continue
		}
		// not made yet: reserved by name below the folder it will be made in
		if len(ancestors) > 0 && os.SameFile(ancestors[0], pfi) && len(tail) >= len(ptail) && foldedPrefix(tail, ptail) {
			return true
		}
	}
	return false
}

func clean(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p != "" && filepath.IsAbs(p) {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

// foldedPrefix reports whether tail starts with prefix, each name compared NFC-normalised and
// case-folded: what a case-insensitive, normalising volume (APFS, HFS+) takes for the same name.
func foldedPrefix(tail, prefix []string) bool {
	for i, p := range prefix {
		if !strings.EqualFold(norm.NFC.String(tail[i]), norm.NFC.String(p)) {
			return false
		}
	}
	return true
}

// candidate is the absolute, clean p as a create would make it: existing, the deepest part of it
// that exists, with every link on the way resolved; tail, the names below it not made yet. A name is
// missing only when the disk says so (ENOENT, ENOTDIR); a link that leads nowhere, a folder without
// search permission or any other error refuses.
func candidate(p string) (existing string, tail []string, err error) {
	comps := strings.Split(strings.TrimPrefix(p, string(filepath.Separator)), string(filepath.Separator))
	cur := string(filepath.Separator)
	for i, c := range comps {
		if c == "" {
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		switch {
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
			return cur, comps[i:], nil
		case err != nil:
			return "", nil, errUnknown
		case fi.Mode()&fs.ModeSymlink != 0:
			r, err := filepath.EvalSymlinks(next)
			if errors.Is(err, fs.ErrNotExist) {
				return "", nil, errDangling
			}
			if err != nil {
				return "", nil, errUnknown
			}
			cur = r
		default:
			cur = next
		}
	}
	return cur, nil, nil
}

var (
	errDangling = errors.New(whyDangling)
	errUnknown  = errors.New(whyUnknown)
)

// withPath is candidate's error as a refusal naming path.
func withPath(err error, path string) error {
	if errors.Is(err, errDangling) {
		return refuse(path, whyDangling)
	}
	return refuse(path, whyUnknown)
}

// within reports whether p is root or below it.
func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator)) || root == string(filepath.Separator)
}
