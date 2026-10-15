package todo_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

func TestAddWritesAutoCoresFile(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "Ship it, now!", Description: "Do the thing.", Priority: "high", Tags: []string{"a"},
		ADR: []string{"/kb/adrs/0001.md", "/home/u/x.md"}})
	if task.ID != "2026-10-11-ship-it-now" || task.Path != "open/2026-10-11-ship-it-now.md" || task.Version != 1 {
		t.Fatalf("%+v", task)
	}
	want := "---\nid: \"2026-10-11-ship-it-now\"\nversion: 1\nstatus: open\ntitle: Ship it, now!\npriority: high\n" +
		"tags:\n  - a\nadr:\n  - $KB_ROOT/adrs/0001.md\n  - $HOME/x.md\ncreated: \"2026-10-11T10:00:00-07:00\"\n" +
		"updated: \"2026-10-11T10:00:00-07:00\"\nstatus_changed: \"2026-10-11T10:00:00-07:00\"\n---\n\n<!-- ─── auto-core.todo"
	got := e.read(kbStore + "/open/2026-10-11-ship-it-now.md")
	if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, " -->\n\n# Ship it, now!\n\nDo the thing.\n") {
		t.Errorf("file:\n%s", got)
	}
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "Ship it, now"}); !is(err, todo.ErrExists) {
		t.Errorf("same id again: %v", err)
	}
	for _, id := range []string{"../escape", "a/b", ".hidden", "with space", ""} {
		if id == "" {
			continue
		}
		if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "x", ID: id}); !is(err, todo.ErrInvalid) {
			t.Errorf("id %q: %v", id, err)
		}
	}
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "x", Priority: "urgent"}); !is(err, todo.ErrInvalid) {
		t.Errorf("a value the schema refuses: %v", err)
	}
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: ""}); !is(err, todo.ErrInvalid) {
		t.Errorf("no title: %v", err)
	}
}

// Every status reaches every other, with auto-core's timestamps; the same status is a no-op.
func TestTransitionsAnyToAny(t *testing.T) {
	for _, from := range todo.Statuses {
		for _, to := range todo.Statuses {
			e := claimed(t)
			task := e.add(todo.Spec{Title: "t", Status: from})
			e.advance(time.Hour)
			rows := len(e.rows())
			got, err := e.m.Status(ctx, kb, task.ID, task.Rev, to)
			if err != nil {
				t.Fatalf("%s → %s: %v", from, to, err)
			}
			if from == to {
				if got.Rev != task.Rev || len(e.rows()) != rows {
					t.Errorf("%s → %s wrote", from, to)
				}
				continue
			}
			now := "2026-10-11T11:00:00-07:00"
			wantCompleted := map[string]string{"completed": now, "archived": ""}[to]
			if to == "archived" && from == "completed" {
				wantCompleted = task.CompletedAt
			}
			wantArchived := map[string]string{"archived": now}[to]
			if got.Status != to || got.StatusChanged != now || got.CompletedAt != wantCompleted || got.ArchivedAt != wantArchived ||
				got.Updated != task.Updated {
				t.Errorf("%s → %s: %+v", from, to, got)
			}
			wantPath := to + "/" + task.ID + ".md"
			if to == "archived" {
				wantPath = "archived/2026/10/" + task.ID + ".md"
			}
			if got.Path != wantPath || !e.exists(kbStore+"/"+wantPath) || e.exists(kbStore+"/"+task.Path) {
				t.Errorf("%s → %s: at %s", from, to, got.Path)
			}
			last := e.rows()[len(e.rows())-1]
			if last.Kind != todo.KindStatus || last.From != from || last.To != to || last.Op != todo.OpMove {
				t.Errorf("%s → %s: row %+v", from, to, last)
			}
		}
	}
}

// D1: assigning an open task moves it to in-progress in the same write; nothing else moves.
func TestAssignD1(t *testing.T) {
	e := claimed(t)
	open := e.add(todo.Spec{Title: "open one"})
	got, err := e.m.Assign(ctx, kb, open.ID, open.Rev, "agent:jarvis", "start with the tests")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in-progress" || got.Assignee != "agent:jarvis" || got.Path != "in-progress/"+open.ID+".md" ||
		e.exists(kbStore+"/open/"+open.ID+".md") {
		t.Fatalf("%+v", got)
	}
	last := e.rows()[len(e.rows())-1]
	if last.Kind != todo.KindAssign || last.To != "agent:jarvis" || last.Reason != "start with the tests" || last.Op != todo.OpMove {
		t.Errorf("row %+v", last)
	}
	// re-assigning never moves it back, and is delivered again
	again, err := e.m.Assign(ctx, kb, got.ID, got.Rev, "agent:jarvis", "actually, the docs first")
	if err != nil || again.Status != "in-progress" || e.rows()[len(e.rows())-1].Reason != "actually, the docs first" {
		t.Errorf("re-assign: %+v %v", again, err)
	}
	// unassigning changes no status
	cleared, err := e.m.Assign(ctx, kb, again.ID, again.Rev, "", "")
	if err != nil || cleared.Assignee != "" || cleared.Status != "in-progress" {
		t.Errorf("unassign: %+v %v", cleared, err)
	}
	rows := len(e.rows())
	if _, err := e.m.Assign(ctx, kb, cleared.ID, cleared.Rev, "", ""); err != nil || len(e.rows()) != rows {
		t.Errorf("clearing an unassigned task wrote: %v", err)
	}
	for _, status := range []string{"deferred", "completed", "in-progress", "archived"} {
		task := e.add(todo.Spec{Title: "s " + status, Status: status})
		got, err := e.m.Assign(ctx, kb, task.ID, task.Rev, "agent:x", "")
		if err != nil || got.Status != status {
			t.Errorf("assign a %s task: %+v %v", status, got, err)
		}
	}
}

func TestUpdatePatchesHandEditableFieldsOnly(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "t", Tags: []string{"a"}, Due: "2026-11-01"})
	e.advance(time.Minute)
	got, err := e.m.Update(ctx, kb, task.ID, task.Rev, todo.Patch{"title": "new", "tags": []any{"b", "c"}, "due": nil,
		"review": "/kb/r.md", "description": "body"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new" || !reflect.DeepEqual(got.Tags, []string{"b", "c"}) || got.Due != "" || got.Description != "body" ||
		!reflect.DeepEqual(got.Review, []string{"$KB_ROOT/r.md"}) || got.Updated != "2026-10-11T10:01:00-07:00" {
		t.Errorf("%+v", got)
	}
	last := e.rows()[len(e.rows())-1]
	if want := []string{"title", "due", "tags", "review", "updated", "description"}; last.Kind != todo.KindUpdate || !reflect.DeepEqual(last.Fields, want) {
		t.Errorf("row %+v, want fields %v", last, want)
	}
	for _, p := range []todo.Patch{{"created": "2026-01-01T00:00:00Z"}, {"errors": nil}, {"id": "other"}, {"version": 2},
		{"status": "completed"}, {"assignee": "x"}, {"colour": "red"}, {"title": ""}, {"priority": "urgent"}, {"tags": 7}} {
		if _, err := e.m.Update(ctx, kb, got.ID, got.Rev, p); !is(err, todo.ErrInvalid) {
			t.Errorf("patch %v: %v", p, err)
		}
	}
	if e.get(got.ID).Rev != got.Rev {
		t.Error("a refused patch wrote")
	}
}

func TestStaleRevIsRefused(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "t"})
	moved, err := e.m.Update(ctx, kb, task.ID, task.Rev, todo.Patch{"title": "v2"})
	if err != nil {
		t.Fatal(err)
	}
	rows := len(e.rows())
	for name, call := range map[string]func() error{
		"update": func() error { _, err := e.m.Update(ctx, kb, task.ID, task.Rev, todo.Patch{"title": "v3"}); return err },
		"status": func() error { _, err := e.m.Status(ctx, kb, task.ID, task.Rev, "deferred"); return err },
		"assign": func() error { _, err := e.m.Assign(ctx, kb, task.ID, task.Rev, "x", ""); return err },
		"remove": func() error { return e.m.Remove(ctx, kb, task.ID, task.Rev) },
	} {
		if err := call(); !is(err, todo.ErrStale) {
			t.Errorf("%s with a stale rev: %v", name, err)
		}
	}
	if len(e.rows()) != rows || e.get(task.ID).Rev != moved.Rev {
		t.Error("a stale write changed something")
	}
}

// A hand edit made after the daemon read the file, keeping its size, is caught by the conditional
// write and kept.
func TestHandEditBetweenReadAndWriteIsCaught(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "aaaa"})
	p := kbStore + "/" + task.Path
	edited := strings.Replace(e.read(p), "# aaaa", "# bbbb", 1)
	e.disk.SetFault(func(op, name string) error {
		if op == "write" && name == p {
			e.disk.SetFault(nil)
			e.disk.WriteFile(p, []byte(edited)) // the same size
		}
		return nil
	})
	if _, err := e.m.Update(ctx, kb, task.ID, task.Rev, todo.Patch{"title": "cccc"}); !is(err, todo.ErrStale) {
		t.Fatalf("got %v, want ErrStale", err)
	}
	if e.read(p) != edited || len(e.pending()) != 0 {
		t.Error("the hand edit was lost, or the row was left pending")
	}
}

func TestRacingWritesAreSerialized(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "t"})
	const n = 16
	errs := make(chan error, n)
	for i := range n {
		go func() {
			_, err := e.m.Update(ctx, kb, task.ID, task.Rev, todo.Patch{"description": strings.Repeat("x", i+1)})
			errs <- err
		}()
	}
	ok := 0
	for range n {
		if err := <-errs; err == nil {
			ok++
		} else if !is(err, todo.ErrStale) {
			t.Error(err)
		}
	}
	if ok != 1 {
		t.Errorf("%d writes won the same rev, want 1", ok)
	}
	if len(e.pending()) != 0 {
		t.Error("rows left pending")
	}
}

// Refresh: day 28 stays, day 29 archives, into the archived_at month; errors[] is set, kept with
// its detected time, and cleared; refs are symbolized; a misplaced file moves.
func TestRefreshRules(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbStore+"/completed/day28.md", []byte(strings.Replace(taskFile("day28", "completed"),
		"completed_at: \"2026-10-01T09:00:00-07:00\"", "completed_at: \"2026-09-13T23:59:59-07:00\"", 1)))
	e.disk.WriteFile(kbStore+"/completed/day29.md", []byte(strings.Replace(taskFile("day29", "completed"),
		"completed_at: \"2026-10-01T09:00:00-07:00\"", "completed_at: \"2026-09-12T00:00:00-07:00\"", 1)))
	e.disk.WriteFile(kbStore+"/open/refs.md", []byte(taskFile("refs", "open", "adr:\n  - /kb/adrs/a.md\n  - adrs/missing.md",
		"blocked:\n  - day29\n  - nobody")))
	e.disk.WriteFile(kbStore+"/open/misplaced.md", []byte(taskFile("misplaced", "deferred")))
	e.disk.WriteFile("/kb/adrs/a.md", []byte("a"))
	sum, err := e.m.Refresh(ctx, kb)
	if err != nil {
		t.Fatal(err)
	}
	if want := (todo.RefreshSummary{Scanned: 4, Moved: 2, Archived: 1, ErrorsSet: 1, Rewritten: 1}); sum != want {
		t.Errorf("summary %+v, want %+v", sum, want)
	}
	if !e.exists(kbStore+"/completed/day28.md") || !e.exists(kbStore+"/archived/2026/10/day29.md") || e.exists(kbStore+"/completed/day29.md") {
		t.Error("the 28-day rule")
	}
	day29 := e.get("day29")
	if day29.Status != "archived" || day29.CompletedAt != "2026-09-12T00:00:00-07:00" || day29.ArchivedAt != "2026-10-11T10:00:00-07:00" {
		t.Errorf("archived: %+v", day29)
	}
	refs := e.get("refs")
	if !reflect.DeepEqual(refs.ADR, []string{"$KB_ROOT/adrs/a.md", "adrs/missing.md"}) || len(refs.Errors) != 2 ||
		refs.Errors[0].Field != "adr[1]" || refs.Errors[1].Field != "blocked[1]" || refs.Errors[0].Detected != "2026-10-11T10:00:00-07:00" {
		t.Errorf("refs: %+v", refs)
	}
	if e.get("misplaced").Path != "deferred/misplaced.md" {
		t.Error("a misplaced file did not move")
	}
	kinds := map[string]int{}
	for _, c := range e.rows() {
		if !c.Hand {
			kinds[c.Kind]++
		}
	}
	if kinds[todo.KindArchive] != 1 || kinds[todo.KindRefresh] != 2 {
		t.Errorf("rows %v", kinds)
	}
	// a second refresh an hour later keeps detected, and writes nothing
	e.advance(time.Hour)
	before := e.read(kbStore + "/open/refs.md")
	sum, _ = e.m.Refresh(ctx, kb)
	if (sum != todo.RefreshSummary{Scanned: 3}) || e.read(kbStore+"/open/refs.md") != before {
		t.Errorf("second refresh %+v", sum)
	}
	// fixing the ref clears its error
	e.disk.WriteFile("/kb/adrs/missing.md", []byte("m"))
	e.m.Refresh(ctx, kb)
	if refs := e.get("refs"); len(refs.Errors) != 1 || refs.Errors[0].Field != "blocked[1]" || refs.Errors[0].Detected != "2026-10-11T10:00:00-07:00" {
		t.Errorf("after the fix: %+v", refs.Errors)
	}
}

func TestMalformedFilesAreListedAndRemovedOnRequest(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbStore+"/open/broken.md", []byte("---\nid: broken\n---\n"))
	e.disk.WriteFile(kbStore+"/open/auto.md", []byte(taskFile("auto", "open", "origin: \"x\"")))
	e.disk.WriteFile(kbStore+"/automated/tmpl.md", []byte("anything"))
	e.disk.WriteFile(kbStore+"/open/good.md", []byte(taskFile("good", "open")))
	l, err := e.m.List(ctx, kb, todo.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, b := range l.Malformed {
		reasons[b.Path] = b.Reason
	}
	if len(l.Tasks) != 1 || len(reasons) != 3 || !strings.Contains(reasons["open/auto.md"], "automation was removed") ||
		!strings.Contains(reasons["automated/tmpl.md"], "automation was removed") || !strings.Contains(reasons["open/broken.md"], "version is required") {
		t.Fatalf("%+v", l)
	}
	for _, p := range []string{"open/good.md", "open/auto.md", "automated/tmpl.md", "../x.md", "open/../../x.md", ".autodoc-owner"} {
		if err := e.m.RemoveMalformed(ctx, kb, p); !is(err, todo.ErrInvalid) {
			t.Errorf("remove_malformed %s: %v", p, err)
		}
	}
	if err := e.m.RemoveMalformed(ctx, kb, "open/nope.md"); !is(err, todo.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if err := e.m.RemoveMalformed(ctx, kb, "open/broken.md"); err != nil {
		t.Fatal(err)
	}
	if e.exists(kbStore + "/open/broken.md") {
		t.Error("not removed")
	}
	last := e.rows()[len(e.rows())-1]
	if last.Kind != todo.KindRemoveMalformed || last.ID != "broken" || last.State != todo.StateCommitted {
		t.Errorf("row %+v", last)
	}
	// refresh never rewrites or deletes what automation left
	auto := e.read(kbStore + "/open/auto.md")
	if sum, err := e.m.Refresh(ctx, kb); err != nil || sum.Malformed != 2 || e.read(kbStore+"/open/auto.md") != auto {
		t.Errorf("refresh: %+v %v", sum, err)
	}
}

// A duplicate an interrupted move left (by a writer with no journal) is resolved by refresh: the
// older copy goes.
func TestRefreshResolvesAnInterruptedMovesDuplicate(t *testing.T) {
	e := claimed(t)
	old := taskFile("dup", "open")
	newer := strings.Replace(strings.Replace(taskFile("dup", "completed"), "status_changed: \"2026-10-01T09:00:00-07:00\"",
		"status_changed: \"2026-10-02T09:00:00-07:00\"", 1), "completed_at: \"2026-10-01T09:00:00-07:00\"", "completed_at: \"2026-10-02T09:00:00-07:00\"", 1)
	e.disk.WriteFile(kbStore+"/open/dup.md", []byte(old))
	e.disk.WriteFile(kbStore+"/completed/dup.md", []byte(newer))
	if _, err := e.m.Refresh(ctx, kb); err != nil {
		t.Fatal(err)
	}
	if e.exists(kbStore+"/open/dup.md") || e.read(kbStore+"/completed/dup.md") != newer {
		t.Error("the older copy should go, the newer stay untouched")
	}
	// two different tasks under one id are left for a person
	e.disk.WriteFile(kbStore+"/open/dup.md", []byte(strings.Replace(old, "Body of dup.", "Different body.", 1)))
	sum, _ := e.m.Refresh(ctx, kb)
	if !e.exists(kbStore+"/open/dup.md") || sum.Malformed != 1 {
		t.Errorf("an unresolvable duplicate: %+v", sum)
	}
}

func TestListFilters(t *testing.T) {
	e := claimed(t)
	e.add(todo.Spec{Title: "a", Tags: []string{"x"}, Priority: "high", Assignee: "jarvis"})
	e.add(todo.Spec{Title: "b", Tags: []string{"y"}})
	e.add(todo.Spec{Title: "c", Status: "deferred", Tags: []string{"x"}})
	count := func(o todo.ListOpts) int {
		l, err := e.m.List(ctx, kb, o)
		if err != nil {
			t.Fatal(err)
		}
		return len(l.Tasks)
	}
	if count(todo.ListOpts{}) != 3 || count(todo.ListOpts{Tag: "x"}) != 2 || count(todo.ListOpts{Priority: "high"}) != 1 ||
		count(todo.ListOpts{Assignee: "jarvis"}) != 1 || count(todo.ListOpts{Buckets: []string{"deferred"}}) != 1 ||
		count(todo.ListOpts{HasErrors: true}) != 0 {
		t.Error("filters")
	}
	if _, err := e.m.List(ctx, kb, todo.ListOpts{Buckets: []string{"archived/26/1"}}); !is(err, todo.ErrInvalid) {
		t.Errorf("a bad bucket: %v", err)
	}
}

func TestVars(t *testing.T) {
	e := claimed(t)
	e.must(e.m.SetVar(ctx, "PROJ", "/home/u/src/proj"))
	for _, name := range []string{"KB_ROOT", "WORKSPACE", "PROJECT_ROOT", "HOME", "1BAD", "a-b"} {
		if err := e.m.SetVar(ctx, name, "/x"); !is(err, todo.ErrInvalid) {
			t.Errorf("SetVar(%s): %v", name, err)
		}
	}
	vars, err := e.m.Vars(ctx, kb)
	if err != nil {
		t.Fatal(err)
	}
	want := []todo.Var{{Name: "KB_ROOT", Value: "/kb", Builtin: true, Derived: "/kb", Source: "derived"},
		{Name: "WORKSPACE", Value: "/kb", Builtin: true, Derived: "/kb", Source: "derived"},
		{Name: "PROJECT_ROOT", Builtin: true},
		{Name: "HOME", Value: "/home/u", Builtin: true, Derived: "/home/u", Source: "derived"},
		{Name: "PROJ", Value: "/home/u/src/proj", Source: "global"}}
	if !reflect.DeepEqual(vars, want) {
		t.Errorf("%+v", vars)
	}
	if task := e.add(todo.Spec{Title: "t", ADR: []string{"/home/u/src/proj/d.md"}}); !slices.Equal(task.ADR, []string{"$PROJ/d.md"}) {
		t.Errorf("a user var: %v", task.ADR)
	}
	e.must(e.m.SetVar(ctx, "PROJ", ""))
	if vars, _ := e.m.Vars(ctx, kb); len(vars) != 4 {
		t.Error("clearing a var")
	}
}
