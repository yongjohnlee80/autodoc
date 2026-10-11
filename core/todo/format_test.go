package todo

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/schema"
)

// Golden files are auto-core's own md.encode(md.decode(f)) of each testdata/render input,
// produced by its Lua (v0.3.2) under luajit: Go's rendering must equal them byte for byte.
func TestRenderMatchesAutoCore(t *testing.T) {
	inputs, _ := filepath.Glob("testdata/render/*.md")
	if len(inputs) == 0 {
		t.Fatal("no fixtures")
	}
	for _, in := range inputs {
		src, _ := os.ReadFile(in)
		want, _ := os.ReadFile(filepath.Join("testdata/golden", filepath.Base(in)))
		task, err := decode(src)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got := encode(task); !bytes.Equal(got, want) {
			t.Errorf("%s: rendering differs from auto-core's:\n%s\n--- want ---\n%s", in, got, want)
		}
		// rendering is a fixed point: what the daemon writes reads back as itself. A title spanning
		// lines is the exception, in auto-core as here: its H1 takes only its first line back.
		if strings.Contains(task.Title, "\n") {
			continue
		}
		again, err := decode(want)
		if err != nil || !bytes.Equal(encode(again), want) {
			t.Errorf("%s: golden does not round-trip: %v", in, err)
		}
	}
}

// The field order comes from the format's schema: reordering formats/v1.yaml's order list changes
// the rendering, so the golden test above would fail.
func TestOrderComesFromTheSchema(t *testing.T) {
	src, err := formatFiles.ReadFile("formats/v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	mutant := strings.Replace(string(src), "order: [id, version, status, title,", "order: [id, version, title, status,", 1)
	if mutant == string(src) {
		t.Fatal("the order list moved; update this test")
	}
	s, err := schema.Parse([]byte(mutant))
	if err != nil {
		t.Fatal(err)
	}
	f := &format{version: 1, schema: s, order: s.Order(), check: checkV1}
	in, _ := os.ReadFile("testdata/render/full.md")
	want, _ := os.ReadFile("testdata/golden/full.md")
	task, err := decode(in)
	if err != nil {
		t.Fatal(err)
	}
	got := f.encode(task)
	if bytes.Equal(got, want) {
		t.Fatal("a reordered schema rendered the same bytes: the order is not the schema's")
	}
	if !bytes.Contains(got, []byte("version: 1\ntitle: \"Fix: the parser's #1 bug\"\nstatus: completed\n")) {
		t.Errorf("not in the schema's order:\n%s", got)
	}
}

// The embedded v1 format declares exactly auto-core's schema.lua fields minus automation, in its
// FRONTMATTER_ORDER, with its enums, required and managed fields.
func TestFormatV1IsAutoCoresSchema(t *testing.T) {
	f := formats[1]
	var names []string
	for _, fd := range f.schema.Fields {
		names = append(names, fd.Name)
	}
	slices.Sort(names)
	want := []string{"adr", "archived_at", "assignee", "blocked", "completed_at", "created", "description", "due", "errors",
		"id", "priority", "review", "status", "status_changed", "tags", "title", "updated", "version"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("fields = %v\nwant     %v", names, want)
	}
	order := []string{"id", "version", "status", "title", "due", "priority", "assignee", "tags", "adr", "review", "blocked",
		"created", "updated", "status_changed", "completed_at", "archived_at", "errors"}
	if !reflect.DeepEqual(f.order, order) {
		t.Errorf("order = %v", f.order)
	}
	required := map[string]bool{"id": true, "version": true, "created": true, "updated": true, "status_changed": true, "status": true, "title": true}
	managed := map[string]bool{"id": true, "version": true, "created": true, "updated": true, "status_changed": true,
		"completed_at": true, "archived_at": true, "errors": true}
	for _, fd := range f.schema.Fields {
		if fd.Required != required[fd.Name] || fd.Managed != managed[fd.Name] {
			t.Errorf("%s: required %v managed %v", fd.Name, fd.Required, fd.Managed)
		}
	}
	enum := func(name string) []string { fd, _ := f.schema.Field(name); return fd.Enum }
	if e := enum("status"); !reflect.DeepEqual(e, Statuses) {
		t.Errorf("status enum = %v", e)
	}
	if e := enum("priority"); !reflect.DeepEqual(e, []string{"low", "normal", "high"}) {
		t.Errorf("priority enum = %v", e)
	}
	errs, _ := f.schema.Field("errors")
	var items []string
	for _, it := range errs.Items {
		items = append(items, it.Name)
		if it.Name == "code" && !reflect.DeepEqual(it.Enum, []string{CodeNotFound, CodeUnresolved}) {
			t.Errorf("errors[].code enum = %v", it.Enum)
		}
	}
	if errs.ItemType != schema.Map || !reflect.DeepEqual(items, []string{"field", "code", "message", "detected"}) {
		t.Errorf("errors items = %v", items)
	}
	if !f.schema.Strict {
		t.Error("v1 is closed: strict")
	}
}

// Go and auto-core's Lua agree on every malformed fixture (testdata/malformed/lua-verdicts.txt,
// produced by auto-core v0.3.2), but for the deliberate differences: null is absent in AutoDoc,
// and automation, which auto-core v0.3.2 still reads, is gone.
func TestMalformedAgreesWithLua(t *testing.T) {
	verdicts, err := os.ReadFile("testdata/malformed/lua-verdicts.txt")
	if err != nil {
		t.Fatal(err)
	}
	differs := map[string]string{
		"null-assignee.md":     "valid",
		"null-due.md":          "valid",
		"automation-origin.md": automationRemoved,
		"automation-status.md": automationRemoved,
	}
	for _, line := range strings.Split(strings.TrimSpace(string(verdicts)), "\n") {
		name, lua, _ := strings.Cut(line, " ")
		src, _ := os.ReadFile(filepath.Join("testdata/malformed", name))
		_, err := decode(src)
		got := "valid"
		if err != nil {
			got = "malformed"
		}
		switch want, ok := differs[name]; {
		case ok && want == "valid":
			if err != nil {
				t.Errorf("%s: %v, want valid (null is absent)", name, err)
			}
		case ok:
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %v, want %q", name, err, want)
			}
		case got != lua:
			t.Errorf("%s: Go says %s (%v), auto-core says %s", name, got, err, lua)
		}
	}
	src, _ := os.ReadFile("testdata/malformed/version-2.md")
	if _, err := decode(src); err == nil || err.Error() != "format version 2 is newer than this AutoDoc" {
		t.Errorf("version 2: %v", err)
	}
}

func TestDecodeReadsTolerantly(t *testing.T) {
	src, _ := os.ReadFile("testdata/render/handwritten.md")
	task, err := decode(src)
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "Hand written" || !reflect.DeepEqual(task.Tags, []string{"solo-tag"}) ||
		!reflect.DeepEqual(task.ADR, []string{"a/b.md", "c d.md"}) || task.ID != "2026-06-01-hand-written" {
		t.Errorf("%+v", task)
	}
	if task.Description != "First paragraph.\n\n\nTrailing blank lines above and below." {
		t.Errorf("description = %q", task.Description)
	}
	// an anchor-like line inside a block scalar is text, as auto-core reads it
	block := "---\nid: a\nversion: 1\nstatus: open\ntitle: |\n  &not an anchor\n  *nor an alias\ncreated: \"2026-01-01T00:00:00Z\"\n" +
		"updated: \"2026-01-01T00:00:00Z\"\nstatus_changed: \"2026-01-01T00:00:00Z\"\n---\n"
	if task, err := decode([]byte(block)); err != nil || task.Title != "&not an anchor\n*nor an alias\n" {
		t.Errorf("block scalar: %q %v", task.Title, err)
	}
	// unquoted dates stay strings
	if task, err := decode([]byte(strings.Replace(block, "status: open", "status: open\ndue: 2026-06-01", 1))); err != nil || task.Due != "2026-06-01" {
		t.Errorf("due: %q %v", task.Due, err)
	}
}

func TestMakeIDIsAutoCores(t *testing.T) {
	// auto-core's paths.make_id under luajit, for the same titles
	for title, want := range map[string]string{
		"Hello, World!": "hello-world", "  multiple   spaces  ": "multiple-spaces", "Café & Crème": "caf-crme",
		"---dashes---": "dashes", "": "untitled", "UPPER_case_underscore": "uppercaseunderscore",
		"tabs\tand\nnewlines": "tabs-and-newlines", "a - b -- c": "a-b-c", "Fix: the parser's #1 bug": "fix-the-parsers-1-bug",
		"!!!": "untitled",
	} {
		if got := makeID("2026-10-11T10:00:00-07:00", title); got != "2026-10-11-"+want {
			t.Errorf("makeID(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestAgedOutBoundary(t *testing.T) {
	cases := []struct {
		completed, now string
		out            bool
	}{
		{"2026-09-13T23:59:59-07:00", "2026-10-11T00:00:01-07:00", false}, // day 28 stays
		{"2026-09-12T00:00:00-07:00", "2026-10-11T23:00:00-07:00", true},  // day 29 goes
		{"2026-02-01T10:00:00+09:00", "2026-03-01T10:00:00+09:00", false}, // 28 days in February
		{"2026-02-01T10:00:00+09:00", "2026-03-02T10:00:00+09:00", true},
		{"garbage", "2026-03-02T10:00:00+09:00", false},
	}
	for _, c := range cases {
		if got := agedOut(c.completed, c.now); got != c.out {
			t.Errorf("agedOut(%s, %s) = %v", c.completed, c.now, got)
		}
	}
}

func TestStatusTimestamps(t *testing.T) {
	const now = "2026-10-11T10:00:00-07:00"
	base := Task{CompletedAt: "2026-01-01T00:00:00Z", ArchivedAt: "2026-02-01T00:00:00Z"}
	for _, c := range []struct {
		from, to, completed, archived string
	}{
		{"completed", "archived", "2026-01-01T00:00:00Z", now}, // keeps completed_at only from completed
		{"open", "archived", "", now},
		{"archived", "completed", now, ""},
		{"completed", "open", "", ""},
		{"archived", "in-progress", "", ""},
		{"completed", "deferred", "", ""},
	} {
		task := base
		task.Status = c.from
		setStatus(&task, c.to, now)
		if task.Status != c.to || task.StatusChanged != now || task.CompletedAt != c.completed || task.ArchivedAt != c.archived {
			t.Errorf("%s → %s: %+v", c.from, c.to, task)
		}
	}
}

func TestSymbolizeLongestPrefix(t *testing.T) {
	sc := scope{kbRoot: "/home/u/kb", workspace: "/home/u/kb", home: "/home/u", user: map[string]string{"PROJ": "/home/u/src/proj"},
		exists: func(p string) bool { return p == "/home/u/kb/adrs/x.md" }}
	for in, want := range map[string]string{
		"/home/u/kb/adrs/x.md":  "$KB_ROOT/adrs/x.md", // KB_ROOT before WORKSPACE on a tie
		"/home/u/src/proj/a.md": "$PROJ/a.md",         // a user var beats HOME: longer
		"~/notes/n.md":          "$HOME/notes/n.md",
		"/home/u":               "$HOME",
		"/etc/hosts":            "/etc/hosts",
		"adrs/x.md":             "$KB_ROOT/adrs/x.md", // relative refs are the KB's (D2)
		"missing/y.md":          "missing/y.md",
		"$WORKSPACE/already.md": "$WORKSPACE/already.md",
		"/home/u/kbother/z.md":  "$HOME/kbother/z.md", // a prefix must end at a separator
	} {
		if got := sc.symbolize(in); got != want {
			t.Errorf("symbolize(%q) = %q, want %q", in, got, want)
		}
	}
	// a ref under the workspace but not the KB is not resolved against the workspace (D2)
	sc2 := scope{kbRoot: "/kb", workspace: "/ws", exists: func(p string) bool { return p == "/ws/a.md" }}
	if got := sc2.symbolize("a.md"); got != "a.md" {
		t.Errorf("D2: %q", got)
	}
	errs := sc2.refErrors(Task{ADR: []string{"a.md"}}, "now", func(string) bool { return true })
	if len(errs) != 1 || errs[0].Message != "path 'a.md' does not exist (resolved: /kb/a.md)" {
		t.Errorf("D2 errors: %+v", errs)
	}
}

func TestRefErrorsKeepDetected(t *testing.T) {
	sc := scope{kbRoot: "/kb", exists: func(string) bool { return false }}
	task := Task{ADR: []string{"$NOPE/a.md", "gone.md"}, Blocked: []string{"2026-01-01-x"},
		Errors: []ErrorEntry{{Field: "adr[1]", Code: CodeNotFound, Message: "old", Detected: "2026-01-01T00:00:00Z"}}}
	got := sc.refErrors(task, "NOW", func(string) bool { return false })
	want := []ErrorEntry{
		{Field: "adr[0]", Code: CodeUnresolved, Message: "variable '$NOPE' is not defined on this machine — set it in the Vars section or as an environment variable", Detected: "NOW"},
		{Field: "adr[1]", Code: CodeNotFound, Message: "path 'gone.md' does not exist (resolved: /kb/gone.md)", Detected: "2026-01-01T00:00:00Z"},
		{Field: "blocked[0]", Code: CodeNotFound, Message: "Task '2026-01-01-x' does not exist in this workspace", Detected: "NOW"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}
