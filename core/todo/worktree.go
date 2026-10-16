package todo

import (
	"path"
	"strings"
)

// REFS INTO A GIT WORKTREE THAT IS GONE. Agents link a design doc from inside the feature worktree
// they work in; the worktree is removed after its merge, and the ref stops resolving although the
// doc is in the repository's main worktree, at the same path within it. movedFromWorktree finds that
// copy for the two layouts worktrees live in here:
//
//   - a bare repository's container: <repo>/.git is bare and each worktree is a directory beside it
//     (<repo>/main, <repo>/<feature>); a missing <repo>/<feature> is a removed worktree;
//   - a Claude Code worktree: <repo>/.claude/worktrees/<name>, inside an ordinary checkout or a bare
//     repository's container; its main worktree is the checkout, or the container's main. It is
//     known by its path, since removing the last worktree can remove .claude/worktrees too.
//
// Anything else is left alone: a directory deleted from an ordinary checkout is not a worktree, and
// the same path under the repository's root is no evidence of where its file went.

// smallFileLimit bounds what scope.read returns: git's HEAD, gitdir and config files are far smaller.
const smallFileLimit = 64 << 10

// mainBranches are the branches a main worktree is on, in the order one is chosen.
var mainBranches = []string{"main", "master"}

// movedFromWorktree is abs's copy in its repository's main worktree, when abs does not exist and lies
// in a removed worktree of one of the two layouts. ok is false otherwise, or when the main worktree has
// no file at that path.
func (s scope) movedFromWorktree(abs string) (string, bool) {
	if s.exists == nil || s.read == nil || s.list == nil || !strings.HasPrefix(abs, "/") {
		return "", false
	}
	abs = path.Clean(abs)
	if s.exists(abs) {
		return "", false
	}
	if repo, rest, ok := strings.Cut(abs, "/.claude/worktrees/"); ok {
		name, rel, ok := strings.Cut(rest, "/")
		if !ok || name == "" || rel == "" || !s.exists(repo) || s.exists(repo+"/.claude/worktrees/"+name) {
			return "", false // not a path in a removed Claude worktree
		}
		return s.inMain(s.mainWorktreeOf(repo), rel)
	}
	// gone is the topmost missing directory on abs's path: the worktree, when abs was in one
	gone := abs
	for {
		parent := path.Dir(gone)
		if parent == gone {
			return "", false
		}
		if s.exists(parent) {
			break
		}
		gone = parent
	}
	if gone == abs {
		return "", false // only the file is missing: no directory was removed
	}
	parent := path.Dir(gone)
	if !s.isBareContainer(parent) {
		return "", false
	}
	return s.inMain(s.mainWorktreeOfBare(parent+"/.git"), strings.TrimPrefix(abs, gone+"/"))
}

// mainWorktreeOf is repo's main worktree: its container's main for a bare repository's container, the
// checkout itself for an ordinary one, "" for neither.
func (s scope) mainWorktreeOf(repo string) string {
	switch {
	case s.isBareContainer(repo):
		return s.mainWorktreeOfBare(repo + "/.git")
	case s.exists(repo + "/.git"):
		return repo
	}
	return ""
}

// inMain is rel in the main worktree main, when main is known and has a file there.
func (s scope) inMain(main, rel string) (string, bool) {
	if main == "" {
		return "", false
	}
	to := main + "/" + rel
	if !s.exists(to) {
		return "", false
	}
	return to, true
}

// isBareContainer reports dir holding a bare repository as .git, whose worktrees sit beside it.
func (s scope) isBareContainer(dir string) bool {
	cfg, ok := s.read(dir + "/.git/config")
	if !ok {
		return false
	}
	for _, line := range strings.Split(string(cfg), "\n") {
		k, v, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && strings.TrimSpace(k) == "bare" && strings.EqualFold(strings.TrimSpace(v), "true") {
			return true
		}
	}
	return false
}

// mainWorktreeOfBare is the worktree of the bare repository gitDir that has a main branch checked
// out, from git's own records: worktrees/<name>/HEAD and worktrees/<name>/gitdir. "" for none.
func (s scope) mainWorktreeOfBare(gitDir string) string {
	byBranch := map[string]string{}
	for _, name := range s.list(gitDir + "/worktrees") {
		meta := gitDir + "/worktrees/" + name
		head, ok := s.read(meta + "/HEAD")
		if !ok {
			continue
		}
		ref, isRef := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
		if !isRef {
			continue // a detached HEAD is not a branch's worktree
		}
		dotGit, ok := s.read(meta + "/gitdir")
		if !ok {
			continue
		}
		dir := path.Dir(strings.TrimSpace(string(dotGit))) // gitdir names <worktree>/.git
		if strings.HasPrefix(dir, "/") && s.exists(dir) {
			byBranch[ref] = dir
		}
	}
	for _, b := range mainBranches {
		if dir := byBranch[b]; dir != "" {
			return dir
		}
	}
	return ""
}

// worktreeRefs are t's adr and review refs that resolve to a missing path in a removed worktree,
// with where their copy is now, as each would be written: symbolized like any written ref.
func (s scope) worktreeRefs(t Task, file string) []RefCandidate {
	var out []RefCandidate
	for _, f := range []struct {
		name string
		list []string
	}{{"adr", t.ADR}, {"review", t.Review}} {
		for i, p := range f.list {
			r := s.resolve(p)
			if r.unresolved || !strings.HasPrefix(r.path, "/") {
				continue
			}
			to, ok := s.movedFromWorktree(r.path)
			if !ok {
				continue
			}
			out = append(out, RefCandidate{ID: t.ID, Path: file, Field: f.name, Index: i, Ref: p,
				To: s.symbolize(to), Kind: RefWorktree, Suggested: true})
		}
	}
	return out
}
