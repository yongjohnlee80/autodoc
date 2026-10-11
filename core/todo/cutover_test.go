package todo_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// Creating with an assignee is an assignment: an open task starts in-progress, and the change is
// an assign the notifier delivers, made by a create.
func TestAddWithAnAssigneeIsAnAssignment(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "for jarvis", Assignee: "agent:jarvis"})
	if task.Status != "in-progress" || task.Path != "in-progress/"+task.ID+".md" || task.Assignee != "agent:jarvis" {
		t.Fatalf("%+v", task)
	}
	last := e.rows()[len(e.rows())-1]
	if last.Kind != todo.KindAssign || last.Op != todo.OpCreate || last.From != "" || last.To != "agent:jarvis" {
		t.Errorf("row %+v", last)
	}
	open := e.add(todo.Spec{Title: "explicitly open", Status: "open", Assignee: "agent:x"})
	deferred := e.add(todo.Spec{Title: "deferred", Status: "deferred", Assignee: "agent:x"})
	plain := e.add(todo.Spec{Title: "nobody's"})
	if open.Status != "in-progress" || deferred.Status != "deferred" || plain.Status != "open" {
		t.Errorf("statuses %s %s %s", open.Status, deferred.Status, plain.Status)
	}
	rows := e.rows()
	if k := rows[len(rows)-2].Kind; k != todo.KindAssign {
		t.Errorf("a deferred task created with an assignee: %s", k)
	}
	if k := rows[len(rows)-1].Kind; k != todo.KindAdd {
		t.Errorf("a task created with no assignee: %s", k)
	}
}

// The first write to a store the daemon has not listed yet does not make its other files hand adds.
func TestTheFirstWriteKeepsTheBaseline(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{"a", "b"} {
		e.disk.WriteFile(kbStore+"/open/"+id+".md", []byte(taskFile(id, "open")))
	}
	e.must(e.m.Claim(ctx, kb))
	e.add(todo.Spec{Title: "c"})
	e.m.List(ctx, kb, todo.ListOpts{})
	for _, c := range e.rows() {
		if c.Hand {
			t.Errorf("logged as a hand edit: %+v", c)
		}
	}
}

// The cutover's claim rewrites the refs auto-core wrote against Neovim's project root: only
// $WORKSPACE refs that the store's workspace lacks and the legacy root holds, archives included,
// each task one journaled rewrite; a second claim rewrites nothing.
func TestClaimRewritesLegacyWorkspaceRefs(t *testing.T) {
	e := newEnv(t)
	legacy := "/home/u/src/plugins"
	for _, p := range []string{legacy + "/golib/x.md", legacy + "/golib/r.md", legacy + "/vars/v.md", "/kb/adrs/kept.md", legacy + "/adrs/kept.md"} {
		e.disk.WriteFile(p, []byte("x"))
	}
	e.must(e.m.SetVar(ctx, "VARS", legacy+"/vars"))
	errs := "errors:\n  - code: not-found\n    detected: \"2026-09-01T00:00:00Z\"\n    field: adr[0]\n    message: old"
	active := taskFile("a", "open", "adr:\n  - $WORKSPACE/golib/x.md\n  - $WORKSPACE/adrs/kept.md\n  - $WORKSPACE/nowhere.md\n  - ${WORKSPACE}/vars/v.md",
		"review:\n  - ${WORKSPACE}/golib/r.md")
	active = strings.Replace(active, "---\n\n#", errs+"\n---\n\n#", 1)
	archived := strings.Replace(taskFile("b", "archived", "review:\n  - $WORKSPACE/golib/x.md"),
		"archived_at: \"2026-10-01T09:00:00-07:00\"", "archived_at: \"2026-05-01T09:00:00-07:00\"", 1)
	untouched := taskFile("c", "open", "adr:\n  - $KB_ROOT/adrs/kept.md")
	e.disk.WriteFile(kbStore+"/open/a.md", []byte(active))
	e.disk.WriteFile(kbStore+"/archived/2026/05/b.md", []byte(archived))
	e.disk.WriteFile(kbStore+"/open/c.md", []byte(untouched))

	e.must(e.m.Claim(ctx, kb, todo.ClaimOpts{LegacyWorkspace: legacy}))
	a, b := e.get("a"), e.get("b")
	if want := []string{"$HOME/src/plugins/golib/x.md", "$WORKSPACE/adrs/kept.md", "$WORKSPACE/nowhere.md", "$VARS/v.md"}; !reflect.DeepEqual(a.ADR, want) {
		t.Errorf("adr %v\nwant %v", a.ADR, want)
	}
	if !reflect.DeepEqual(a.Review, []string{"$HOME/src/plugins/golib/r.md"}) || !reflect.DeepEqual(b.Review, []string{"$HOME/src/plugins/golib/x.md"}) {
		t.Errorf("review %v %v", a.Review, b.Review)
	}
	if a.Updated != "2026-10-01T09:00:00-07:00" || len(a.Errors) != 1 || a.Errors[0].Message != "old" {
		t.Errorf("the cutover changed more than refs: %+v", a)
	}
	if e.read(kbStore+"/open/c.md") != untouched {
		t.Error("a task with no legacy ref was rewritten")
	}
	var cut []todo.Change
	for _, c := range e.rows() {
		if c.Hand {
			t.Errorf("hand row %+v", c)
		}
		if c.Reason == "cutover: $WORKSPACE was "+legacy {
			cut = append(cut, c)
		}
	}
	if len(cut) != 2 || cut[0].Kind != todo.KindRefresh || cut[0].Op != todo.OpInPlace {
		t.Fatalf("rows %+v", cut)
	}
	before := []string{e.read(kbStore + "/open/a.md"), e.read(kbStore + "/archived/2026/05/b.md")}
	rows := len(e.rows())
	e.must(e.m.Claim(ctx, kb, todo.ClaimOpts{LegacyWorkspace: legacy}))
	e.must(e.m.Claim(ctx, kb))
	if len(e.rows()) != rows || e.read(kbStore+"/open/a.md") != before[0] || e.read(kbStore+"/archived/2026/05/b.md") != before[1] {
		t.Error("a second cutover changed something")
	}
	if err := e.m.Claim(ctx, kb, todo.ClaimOpts{LegacyWorkspace: "relative/dir"}); !is(err, todo.ErrInvalid) {
		t.Errorf("a relative legacy workspace: %v", err)
	}
}
