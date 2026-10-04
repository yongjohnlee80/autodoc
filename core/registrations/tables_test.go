package registrations

import (
	"slices"
	"testing"
)

// TestTheFingerprint: one function, sorted and length-prefixed: the order entries were added in
// does not move it, any single change does, fields cannot run into each other, and the community
// build's empty tables hash the empty input.
func TestTheFingerprint(t *testing.T) {
	if got := (Tables{}).Fingerprint(); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("the empty tables' fingerprint is %s", got)
	}
	if (Tables{}).Fingerprint() != (*Table)(nil).Tables().Fingerprint() {
		t.Fatal("the nil Table's tables are not the empty ones")
	}
	a := Tables{Chunkers: map[string]string{}, Formats: map[string]Format{}}
	b := Tables{Chunkers: map[string]string{}, Formats: map[string]Format{}}
	exts := []string{".go", ".rs", ".py", ".ts", ".c"}
	for i, e := range exts {
		a.Chunkers[e] = "v1"
		b.Chunkers[exts[len(exts)-1-i]] = "v1"
	}
	a.Formats[".pdf"], b.Formats[".pdf"] = Format{"autorag/pdf", "1"}, Format{"autorag/pdf", "1"}
	base := a.Fingerprint()
	if b.Fingerprint() != base {
		t.Fatal("the insertion order moves the fingerprint")
	}
	for _, change := range []func(*Tables){
		func(t *Tables) { t.Chunkers[".go"] = "v2" },
		func(t *Tables) { t.Chunkers[".java"] = "v1" },
		func(t *Tables) { delete(t.Chunkers, ".c") },
		func(t *Tables) { t.Formats[".pdf"] = Format{"autorag/pdf", "2"} },
		func(t *Tables) { t.Formats[".pdf"] = Format{"autorag/pd", "f1"} },
		func(t *Tables) { t.Formats[".docx"] = Format{"autorag/docx", "1"} },
	} {
		c := Tables{Chunkers: map[string]string{}, Formats: map[string]Format{}}
		for k, v := range a.Chunkers {
			c.Chunkers[k] = v
		}
		for k, v := range a.Formats {
			c.Formats[k] = v
		}
		change(&c)
		if c.Fingerprint() == base {
			t.Errorf("a change left the fingerprint: %+v", c)
		}
	}
	// a field's bytes never pass for the next field's
	x := Tables{Chunkers: map[string]string{".a": "b.c"}}
	y := Tables{Chunkers: map[string]string{".ab": ".c"}}
	if x.Fingerprint() == y.Fingerprint() {
		t.Fatal("fields run into each other")
	}
}

// TestTheRestartDirection: only a strict subset, version for version, is behind; equal,
// incomparable and wider tables are not.
func TestTheRestartDirection(t *testing.T) {
	community := Tables{}
	pro := Tables{Chunkers: map[string]string{".go": "1", ".rs": "1"}, Formats: map[string]Format{".pdf": {"autorag/pdf", "1"}}}
	goOnly := Tables{Chunkers: map[string]string{".go": "1"}}
	goNewer := Tables{Chunkers: map[string]string{".go": "2", ".rs": "1"}, Formats: map[string]Format{".pdf": {"autorag/pdf", "1"}}}
	for _, c := range []struct {
		daemon, tui Tables
		want        bool
	}{
		{community, pro, true},
		{goOnly, pro, true},
		{pro, pro, false},
		{pro, community, false},
		{pro, goOnly, false},
		{goNewer, pro, false},
		{pro, goNewer, false},
		{community, community, false},
	} {
		if got := c.daemon.StrictSubsetOf(c.tui); got != c.want {
			t.Errorf("daemon %+v, TUI %+v: %v, want %v", c.daemon, c.tui, got, c.want)
		}
	}
	if got := goOnly.Lacks(goNewer); !slices.Equal(got, []string{".go", ".pdf", ".rs"}) {
		t.Errorf("lacks %q", got)
	}
}
