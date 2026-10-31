package todo

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// MIGRATING LUA-ERA $WORKSPACE REFS (docs/decisions/todos.md#retiring-workspace). Before the
// cutover, auto-core wrote $WORKSPACE/… meaning Neovim's project root; the daemon's $WORKSPACE was
// the store's home. A ref's meaning cannot be told from which files exist today (a target may not
// exist yet, or exist in both places), so nothing rewrites them by itself: MigrateRefs plans, a
// person reviews the plan, and the reviewed list is applied, exactly, each rewrite journaled.

// Ref kinds a migration plans (MigrateOpts.Worktrees chooses).
const (
	RefWorkspace = "workspace" // a Lua-era $WORKSPACE/<rest>, to $PROJECT_ROOT/<rest>
	RefWorktree  = "worktree"  // a ref into a removed git worktree, to its copy in the main worktree
)

// RefCandidate is one adr or review ref a migration would rewrite: a $WORKSPACE/<rest> and where <rest>
// exists now, or a ref into a removed worktree and its copy (worktree.go).
type RefCandidate struct {
	Kind           string `json:"kind,omitempty"` // RefWorkspace when empty
	ID             string `json:"id"`
	Path           string `json:"path,omitempty"` // the task file, store-relative
	Field          string `json:"field"`          // adr or review
	Index          int    `json:"index"`
	Ref            string `json:"ref"`
	To             string `json:"to,omitempty"` // $PROJECT_ROOT/<rest>, the rewrite Apply makes
	UnderWorkspace bool   `json:"under_workspace"`
	UnderProject   bool   `json:"under_project"`
	// Suggested is true when <rest> exists under the project alone: the plan proposes it. A ref found
	// under both, or under neither, is listed for a person to decide and never proposed.
	Suggested bool `json:"suggested"`
}

// MigrateOpts is todos.migrate_refs' options: Plan lists the candidates; Apply rewrites exactly the
// listed ones, each still as the plan saw it. Worktrees plans refs into removed git worktrees instead
// of $WORKSPACE refs; an apply takes each entry's Kind.
type MigrateOpts struct {
	Plan      bool           `json:"plan,omitempty"`
	Worktrees bool           `json:"worktrees,omitempty"`
	Apply     []RefCandidate `json:"apply,omitempty"`
}

// MigrateResult is a plan's candidates, or what an apply rewrote and skipped.
type MigrateResult struct {
	Candidates []RefCandidate `json:"candidates,omitempty"`
	Rewritten  int            `json:"rewritten"`
	Skipped    []RefCandidate `json:"skipped,omitempty"` // listed, but no longer as planned: not rewritten
}

// MigrateRefs plans or applies the migration of an At's store's Lua-era $WORKSPACE refs, or of its
// refs into removed git worktrees. $WORKSPACE refs need the store's $PROJECT_ROOT; an apply needs a
// writable, claimed store.
func (m *Manager) MigrateRefs(ctx context.Context, at At, o MigrateOpts) (MigrateResult, error) {
	if o.Plan == (len(o.Apply) > 0) {
		return MigrateResult{}, fmt.Errorf("%w: give plan: true, or apply: the plan's list as reviewed", ErrInvalid)
	}
	w, err := m.resolve(ctx, at)
	if err != nil {
		return MigrateResult{}, err
	}
	if !o.Plan {
		if err := m.mayWrite(ctx, w); err != nil {
			return MigrateResult{}, err
		}
	}
	sc, err := m.scope(ctx, w)
	if err != nil {
		return MigrateResult{}, err
	}
	workspace := (o.Plan && !o.Worktrees) || slices.ContainsFunc(o.Apply, func(c RefCandidate) bool { return c.kind() == RefWorkspace })
	if workspace && sc.projectRoot == "" {
		return MigrateResult{}, fmt.Errorf("%w: the store has no $PROJECT_ROOT (todos.set_var with scope store, or workspace.set_var, sets it)", ErrInvalid)
	}
	sc, done := m.withExists(ctx, sc)
	defer done()
	st, err := m.open(ctx, w)
	if err != nil {
		return MigrateResult{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := m.ready(ctx, st); err != nil {
		return MigrateResult{}, err
	}
	dirs := slices.Clone(activeBuckets)
	parts, err := st.partitions(ctx)
	if err != nil {
		return MigrateResult{}, err
	}
	for _, p := range parts {
		dirs = append(dirs, "archived/"+p)
	}
	var res MigrateResult
	want := map[string][]RefCandidate{} // an apply's entries, by task id
	for _, c := range o.Apply {
		want[c.ID] = append(want[c.ID], c)
	}
	for _, dir := range dirs {
		entries, _, err := st.list(ctx, dir)
		if err != nil {
			return res, err
		}
		for _, fi := range entries {
			f, ver := st.load(ctx, fi.Path)
			if f.bad != "" {
				continue
			}
			var found []RefCandidate
			if !o.Plan || !o.Worktrees {
				found = append(found, sc.workspaceRefs(f.task, fi.Path)...)
			}
			if !o.Plan || o.Worktrees {
				found = append(found, sc.worktreeRefs(f.task, fi.Path)...)
			}
			if o.Plan {
				res.Candidates = append(res.Candidates, found...)
				continue
			}
			listed := want[f.task.ID]
			if len(listed) == 0 {
				continue
			}
			delete(want, f.task.ID)
			t := clone(f.task)
			changed := false
			for _, c := range listed {
				i := slices.IndexFunc(found, func(x RefCandidate) bool {
					return x.kind() == c.kind() && x.Field == c.Field && x.Index == c.Index && x.Ref == c.Ref && x.To == c.To
				})
				if i < 0 {
					res.Skipped = append(res.Skipped, c)
					continue
				}
				list := t.ADR
				if c.Field == "review" {
					list = t.Review
				}
				list[c.Index], changed = found[i].To, true
				res.Rewritten++
			}
			if !changed {
				continue
			}
			back, content, err := render(t)
			if err != nil {
				return res, err
			}
			reason := "migrate_refs: Lua-era $WORKSPACE was " + sc.projectRoot
			if !slices.ContainsFunc(listed, func(c RefCandidate) bool { return c.kind() == RefWorkspace }) {
				reason = "migrate_refs: refs into removed git worktrees, to their main worktree's copies"
			}
			ch := Change{ID: t.ID, Kind: KindRefresh, Reason: reason,
				FromRev: f.task.Rev, From: t.Status, To: t.Status, Fields: changedFields(back, digests(f.task), digests(back))}
			if _, err := m.rewrite(ctx, st, ch, back, ver, content); err != nil {
				return res, err
			}
		}
	}
	for _, left := range want { // listed tasks the store no longer holds
		res.Skipped = append(res.Skipped, left...)
	}
	return res, nil
}

// workspaceRefs are t's adr and review refs $WORKSPACE/<rest>, with where <rest> exists now.
func (s scope) workspaceRefs(t Task, file string) []RefCandidate {
	var out []RefCandidate
	for _, f := range []struct {
		name string
		list []string
	}{{"adr", t.ADR}, {"review", t.Review}} {
		for i, p := range f.list {
			name, rest := splitVar(p)
			if name != VarWorkspace {
				continue
			}
			rest = strings.TrimLeft(rest, "/")
			c := RefCandidate{ID: t.ID, Path: file, Field: f.name, Index: i, Ref: p, To: "$" + VarProjectRoot + "/" + rest,
				UnderWorkspace: s.exists(joinPath(s.storeHome, rest)), UnderProject: s.exists(joinPath(s.projectRoot, rest))}
			c.Suggested = c.UnderProject && !c.UnderWorkspace
			out = append(out, c)
		}
	}
	return out
}

// kind is c's Kind, RefWorkspace for an entry written before kinds existed.
func (c RefCandidate) kind() string {
	if c.Kind == "" {
		return RefWorkspace
	}
	return c.Kind
}
