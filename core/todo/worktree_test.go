package todo_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// worktreeLayout lays out, under /repo, the two layouts movedFromWorktree knows and the ones it must
// leave alone:
//   - /repo/lm: a bare repository's container, its main worktree /repo/lm/main and a canary
//     worktree beside it, a worktree on a detached HEAD, and Claude Code worktrees under .claude;
//   - /repo/plain: an ordinary checkout with Claude Code worktrees under .claude.
func worktreeLayout(e *env) {
	w := func(p, s string) { e.disk.WriteFile(p, []byte(s)) }
	w("/repo/lm/.git/config", "[core]\n\trepositoryformatversion = 0\n\tbare = true\n")
	for _, wt := range []struct{ name, head string }{
		{"main", "ref: refs/heads/main"}, {"canary", "ref: refs/heads/canary"}, {"detached", "4c8ad8d13cb8378f95d0dc3ef3b3f3ca8d2b20f9"},
	} {
		w("/repo/lm/.git/worktrees/"+wt.name+"/HEAD", wt.head+"\n")
		w("/repo/lm/.git/worktrees/"+wt.name+"/gitdir", "/repo/lm/"+wt.name+"/.git\n")
		w("/repo/lm/"+wt.name+"/.git", "gitdir: /repo/lm/.git/worktrees/"+wt.name+"\n")
	}
	w("/repo/lm/main/docs/a.md", "a")
	w("/repo/lm/canary/docs/a.md", "a")
	w("/repo/lm/canary/docs/only-canary.md", "c")
	w("/repo/lm/.claude/worktrees/live/x.md", "x")
	w("/repo/bare2/.git/config", "[core]\n\tbare = true\n") // no .claude/worktrees left at all
	w("/repo/bare2/.git/worktrees/main/HEAD", "ref: refs/heads/main\n")
	w("/repo/bare2/.git/worktrees/main/gitdir", "/repo/bare2/main/.git\n")
	w("/repo/bare2/main/.git", "gitdir: /repo/bare2/.git/worktrees/main\n")
	w("/repo/bare2/main/docs/c.md", "c")
	w("/repo/plain/.git/config", "[core]\n\tbare = false\n")
	w("/repo/plain/docs/b.md", "b")
	w("/repo/plain/.claude/worktrees/live/x.md", "x")
}

// todos.migrate_refs {worktrees: true} plans each ref into a removed worktree, rewritten to its copy in
// the repository's main worktree, and nothing else; an apply rewrites the reviewed list, journaled,
// skips an entry no longer as planned, and a second plan finds nothing.
func TestMigrateRefsMovesRefsOutOfRemovedWorktrees(t *testing.T) {
	e := claimed(t)
	worktreeLayout(e)
	e.must(e.m.SetVar(ctx, "PROJECT_ROOT", "/repo", todo.SetVarOpts{At: &kb}))
	adr := []string{
		"$PROJECT_ROOT/lm/feat-x/docs/a.md",                    // a bare container's removed worktree: main's, not canary's
		"/repo/lm/.claude/worktrees/gone/docs/a.md",            // a Claude worktree in a bare container
		"$PROJECT_ROOT/lm/feat-y/docs/only-canary.md",          // main has no copy: left
		"$PROJECT_ROOT/lm/main/docs/missing.md",                // only the file is missing: no worktree removed
		"$PROJECT_ROOT/lm/main/docs/a.md",                      // resolves: left
		"$PROJECT_ROOT/plain/gone-dir/docs/b.md",               // a directory deleted from a checkout: not a worktree
		"$PROJECT_ROOT/lm/detached/../gone-detached/docs/x.md", // no main copy: left
		"/repo/bare2/.claude/worktrees/gone/docs/c.md",         // .claude/worktrees itself removed
		"/repo/lm/.claude/worktrees/live/x.md",                 // a live Claude worktree: resolves, left
		"/repo/lm/.claude/worktrees/live/docs/a.md",            // a live worktree's missing file, though main has it: left
	}
	review := []string{"/repo/plain/.claude/worktrees/gone/docs/b.md"} // a Claude worktree in a checkout
	e.disk.WriteFile(kbStore+"/archived/2026/09/w.md", []byte(taskFile("w", "archived",
		"adr:\n  - "+strings.Join(adr, "\n  - ")+"\nreview:\n  - "+review[0])))

	plan, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{Plan: true, Worktrees: true})
	if err != nil {
		t.Fatal(err)
	}
	type move struct{ field, ref, to string }
	var got []move
	for _, c := range plan.Candidates {
		if c.Kind != todo.RefWorktree || !c.Suggested || c.ID != "w" {
			t.Errorf("candidate %+v", c)
		}
		got = append(got, move{c.Field, c.Ref, c.To})
	}
	want := []move{
		{"adr", adr[0], "$PROJECT_ROOT/lm/main/docs/a.md"},
		{"adr", adr[1], "$PROJECT_ROOT/lm/main/docs/a.md"},
		{"adr", adr[7], "$PROJECT_ROOT/bare2/main/docs/c.md"},
		{"review", review[0], "$PROJECT_ROOT/plain/docs/b.md"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan\n%v\nwant\n%v", got, want)
	}
	if ws, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{Plan: true}); err != nil || len(ws.Candidates) != 0 {
		t.Errorf("the $WORKSPACE plan took worktree refs: %+v %v", ws, err)
	}

	stale := plan.Candidates[1]
	stale.To = "$PROJECT_ROOT/lm/canary/docs/a.md" // not what the plan proposed
	apply := []todo.RefCandidate{plan.Candidates[0], stale, plan.Candidates[2], plan.Candidates[3]}
	res, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{Apply: apply})
	if err != nil || res.Rewritten != 3 || len(res.Skipped) != 1 || res.Skipped[0].To != stale.To {
		t.Fatalf("apply %+v %v", res, err)
	}
	w := e.get("w")
	wantADR := slices.Clone(adr)
	wantADR[0] = "$PROJECT_ROOT/lm/main/docs/a.md"
	wantADR[7] = "$PROJECT_ROOT/bare2/main/docs/c.md"
	if !slices.Equal(w.ADR, wantADR) || !slices.Equal(w.Review, []string{"$PROJECT_ROOT/plain/docs/b.md"}) {
		t.Errorf("adr %v\nreview %v", w.ADR, w.Review)
	}
	n := 0
	for _, c := range e.rows() {
		if strings.HasPrefix(c.Reason, "migrate_refs: refs into removed git worktrees") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d journaled worktree migrations, want 1", n)
	}
	again, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{Plan: true, Worktrees: true})
	if err != nil || len(again.Candidates) != 1 || again.Candidates[0].Ref != adr[1] {
		t.Errorf("a second plan %+v %v: only the skipped ref is left", again, err)
	}
}

// A worktree plan needs no $PROJECT_ROOT: its refs are absolute or under any variable.
func TestAWorktreePlanNeedsNoProjectRoot(t *testing.T) {
	e := claimed(t)
	worktreeLayout(e)
	e.disk.WriteFile(kbStore+"/open/p.md", []byte(taskFile("p", "open", "adr:\n  - /repo/lm/feat-z/docs/a.md")))
	plan, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{Plan: true, Worktrees: true})
	if err != nil || len(plan.Candidates) != 1 || plan.Candidates[0].To != "/repo/lm/main/docs/a.md" {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if _, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{Plan: true}); !is(err, todo.ErrInvalid) {
		t.Errorf("a $WORKSPACE plan without PROJECT_ROOT: %v", err)
	}
}

// blocked takes task ids: todos.add and todos.update refuse a sentence there, and accept ids.
func TestBlockedRefusesWhatIsNotATaskID(t *testing.T) {
	e := claimed(t)
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "x", Blocked: []string{"2026-10-15-ok", "waits for the release"}}); !is(err, todo.ErrInvalid) {
		t.Errorf("add with a sentence in blocked: %v", err)
	}
	tk, err := e.m.Add(ctx, kb, todo.Spec{Title: "y", Blocked: []string{"2026-10-15-ok", "other_id"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Update(ctx, kb, tk.ID, tk.Rev, todo.Patch{"blocked": []any{"ADR 0158 step 5"}}); !is(err, todo.ErrInvalid) {
		t.Errorf("update with a sentence in blocked: %v", err)
	}
	if got, err := e.m.Update(ctx, kb, tk.ID, tk.Rev, todo.Patch{"blocked": []any{"2026-10-16-z"}}); err != nil || !slices.Equal(got.Blocked, []string{"2026-10-16-z"}) {
		t.Errorf("update with an id: %+v %v", got.Blocked, err)
	}
}
