package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/registrations"
	serving "github.com/yongjohnlee80/autodoc/internal/daemon"
)

// TestRegisteredTypesOnTheEditTab: the Edit tab offers the daemon's registered types beside the
// built-in ones, filled with those the rules admit. Turning one on includes it and writes the
// code excludes where they are not already there, visible and editable; turning it off drops its
// include and keeps the excludes; a type the daemon does not read is refused, naming what it reads.
func TestRegisteredTypesOnTheEditTab(t *testing.T) {
	b := baseFor(kbInfo())
	b.registered = []string{".go", ".rs"}
	if f := b.form(); f.code != "" {
		t.Fatalf("filled with %q: the rules admit no code", f.code)
	}
	f := b.form()
	f.code = "go"
	got, err := b.changes(f)
	if err != nil {
		t.Fatal(err)
	}
	wantInc := []any{"**/*.md", "**/*.txt", "**/*.yaml", "**/*.yml", "**/*.go"}
	wantExc := []any{".git/**", "vendor/**", "target/**", "dist/**", "build/**"}
	if !reflect.DeepEqual(got["include"], wantInc) || !reflect.DeepEqual(got["exclude"], wantExc) {
		t.Fatalf("turning .go on: %v", got)
	}
	// on already, with some of the excludes: no repeats, and its exclude goes
	on := kbInfo()
	on.include = append(on.include, "**/*.rs")
	on.exclude = []string{"build/**", "**/*.go"}
	b2 := baseFor(on)
	b2.registered = b.registered
	if f := b2.form(); f.code != ".rs" {
		t.Fatalf("filled with %q, want .rs", f.code)
	}
	f2 := b2.form()
	f2.code = ".rs, .go"
	got, err = b2.changes(f2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["exclude"], []any{"build/**", "vendor/**", "target/**", "dist/**"}) || !strings.Contains(strings.Join(anyToStrings(got["include"]), " "), "**/*.go") {
		t.Fatalf("turning .go on beside .rs: %v", got)
	}
	f2.code = ""
	got, err = b2.changes(f2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(anyToStrings(got["include"]), " "), "**/*.rs") || !reflect.DeepEqual(got["exclude"], []any{"build/**", "**/*.go"}) {
		t.Fatalf("turning .rs off: %v", got)
	}
	f.code = ".py"
	if _, err := b.changes(f); err == nil || !strings.Contains(err.Error(), "this backend reads no .py; it reads .go, .rs") {
		t.Fatalf("a type the daemon does not read: %v", err)
	}
	none := baseFor(kbInfo())
	f3 := none.form()
	f3.code = ".go"
	if _, err := none.changes(f3); err == nil || !strings.Contains(err.Error(), "it reads none") {
		t.Fatalf("on a community daemon: %v", err)
	}
	if d := managerDetail(on, false, b.registered); !strings.Contains(d, "file types   Markdown, plain text, YAML, .rs") {
		t.Fatalf("the manager's pane:\n%s", d)
	}
}

func anyToStrings(v any) []string {
	var out []string
	for _, x := range asList(v) {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// fakeChunker is a registration for the daemon's sake: nothing here is cut.
type fakeChunker struct{}

func (fakeChunker) Version() string                          { return "fake-1" }
func (fakeChunker) Chunk(search.Doc) ([]search.Chunk, error) { return nil, nil }

// TestTheEditTabOffersOnlyTheDaemonsTypes: on a registered daemon the Edit tab names its types;
// on a community daemon it offers none, whatever this binary registers.
func TestTheEditTabOffersOnlyTheDaemonsTypes(t *testing.T) {
	for _, c := range []struct {
		name   string
		daemon bool
		shows  bool
	}{{"a registered daemon", true, true}, {"a community daemon, a registered TUI", false, false}} {
		t.Run(c.name, func(t *testing.T) {
			var o serving.Options
			if c.daemon {
				reg, err := registrations.New(map[string]search.Chunker{".go": fakeChunker{}, ".rs": fakeChunker{}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				o.Registrations = reg
			}
			d := startManagedWith(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")}, o)
			r := runTUI(t, NewSession(d.sock, nil), Options{Registrations: proTables})
			r.s.WaitFor(t, "the daemon's kinds", func(string) bool {
				return onLoop(r, func() bool { return r.h.daemonTables.Chunkers != nil })
			})
			r.h.p.Post(func() { r.h.closeDialog("registrations") })
			r.openSettings(t, "kb", 0)
			has := strings.Contains(flat(r.s.String()), "code types this backend reads: .go, .rs")
			if has != c.shows {
				t.Fatalf("the code types shown: %v, want %v\n%s", has, c.shows, r.s.String())
			}
		})
	}
}
