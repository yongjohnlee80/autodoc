package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

const guideFile = "# Guide\n\nintro\n\n## Setup\n\nsteps\n\n### Linux\n\napt\n\n## Usage\n\nrun\n"

// frameTitle is the page frame's title row: the file's name and the breadcrumb.
func (r *running) frameTitle() string {
	for _, line := range strings.Split(r.s.String(), "\n") {
		if strings.Contains(line, "┌ ") {
			return line
		}
	}
	return ""
}

// The breadcrumb on the page's frame follows the cursor through the headings.
func TestTheBreadcrumbFollowsTheCursor(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.openPath("guide.md") })
	r.waitFile(t, "guide.md")
	r.s.WaitFor(t, "the crumb at the first heading", func(string) bool {
		return strings.Contains(r.frameTitle(), "guide.md › Guide")
	})
	r.h.p.Post(func() { r.h.core.SetLine(10, 0) }) // "apt", under Linux
	r.s.WaitFor(t, "the crumb under Linux", func(string) bool {
		return strings.Contains(r.frameTitle(), "guide.md › Guide › Setup › Linux")
	})
	r.keys(t, key('G')) // a key moves it too: the last line, under Usage
	r.s.WaitFor(t, "the crumb under Usage", func(string) bool {
		t := r.frameTitle()
		return strings.Contains(t, "guide.md › Guide › Usage") && !strings.Contains(t, "Linux")
	})
}

// An unsaved heading edit is in the outline, and Enter jumps to where it is now.
func TestTheOutlineFollowsUnsavedEdits(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.openPath("guide.md") })
	r.waitFile(t, "guide.md")
	// a heading added at the top, unsaved: every other heading moves down three lines
	r.h.p.Post(func() {
		r.h.editor.SetValue("# Preface\n\nnew\n\n" + guideFile)
		r.h.edited()
	})
	r.s.WaitFor(t, "the note dirty", func(string) bool { return r.file().dirty })
	r.h.p.Post(func() { r.h.openOutline() })
	r.s.WaitForText(t, "headings (5 of 5)")
	r.s.WaitForText(t, "Preface")
	r.keys(t, decltest.Type("usage")...)
	r.s.WaitForText(t, "headings (1 of 5)")
	r.keys(t, enter())
	r.s.WaitFor(t, "the cursor on Usage's line as it is now", func(string) bool {
		row := onLoop(r, func() int { a, _ := r.h.core.Line(); return a })
		return row == 16 // "## Usage" is line 17 of the edited text (0-based 16)
	})
	if !r.file().dirty {
		t.Fatal("jumping saved or reloaded the note")
	}
	r.s.WaitFor(t, "the crumb at the jump", func(string) bool {
		return strings.Contains(r.frameTitle(), "› Guide › Usage")
	})
}

// A YAML document's breadcrumb is its key path; it has no headings to outline. Plain text has
// neither.
func TestYAMLAndTextBreadcrumbs(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t,
		"conf.yaml", "server:\n  database:\n    host: db\n",
		"notes.txt", "# not a heading\nplain\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.openPath("conf.yaml") })
	r.waitFile(t, "conf.yaml")
	r.h.p.Post(func() { r.h.core.SetLine(2, 6) })
	r.s.WaitFor(t, "the key path", func(string) bool {
		return strings.Contains(r.frameTitle(), "conf.yaml › server › database › host")
	})
	r.h.p.Post(func() { r.h.openOutline() })
	r.waitNoticed(t, "has no headings to navigate: the breadcrumb shows its key path")

	r.h.p.Post(func() { r.h.openPath("notes.txt") })
	r.waitFile(t, "notes.txt")
	if title := r.frameTitle(); strings.Contains(title, "›") {
		t.Fatalf("plain text's frame = %q: no heading crumb from # lines", title)
	}
}
