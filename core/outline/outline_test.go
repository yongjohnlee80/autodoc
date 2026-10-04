package outline

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

const note = `---
title: "# not a heading"
---
# Guide

intro

## Setup *now*

` + "```sh\n# a comment, not a heading\n```" + `

### Linux
text
## Setup now
Setext Heading
--------------
`

func TestMarkdownHeadings(t *testing.T) {
	d := Read([]byte(note), kind.Markdown, "guide")
	var got []string
	for _, h := range d.Headings() {
		got = append(got, strings.Join([]string{h.ID, string(rune('0' + h.Level)), h.Text}, "|"))
	}
	want := []string{"guide|1|Guide", "setup-now|2|Setup now", "linux|3|Linux", "setup-now-1|2|Setup now", "setext-heading|2|Setext Heading"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headings = %v\nwant       %v", got, want)
	}
	hs := d.Headings()
	if hs[0].Line != 4 || hs[2].Line != 14 || !strings.HasPrefix(note[hs[2].Byte:], "### Linux") {
		t.Fatalf("positions: %+v", hs)
	}
}

func TestMarkdownCrumbsFollowTheLine(t *testing.T) {
	d := Read([]byte(note), kind.Markdown, "guide")
	for line, want := range map[int][]string{
		1:  {},
		4:  {"Guide"},
		6:  {"Guide"},
		12: {"Guide", "Setup now"},
		15: {"Guide", "Setup now", "Linux"},
		16: {"Guide", "Setup now"}, // the second "Setup now" pops Linux
		18: {"Guide", "Setext Heading"},
	} {
		if got := d.Crumbs(line, 0); !reflect.DeepEqual(got, want) {
			t.Errorf("line %d: %v, want %v", line, got, want)
		}
	}
}

func TestYAMLCrumbsAreKeyPaths(t *testing.T) {
	src := "server:\n  database:\n    host: db\n    ports:\n      - 1\n      - 2\nname: x\n"
	d := Read([]byte(src), kind.YAML, "conf")
	if len(d.Headings()) != 0 {
		t.Fatalf("YAML has no headings to navigate: %v", d.Headings())
	}
	for _, c := range []struct {
		line, col int
		want      []string
	}{
		{3, 6, []string{"server", "database", "host"}},
		{6, 8, []string{"server", "database", "ports", "[1]"}},
		{7, 0, []string{"name"}},
	} {
		if got := d.Crumbs(c.line, c.col); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%d:%d = %v, want %v", c.line, c.col, got, c.want)
		}
	}
	if got := Read([]byte("a: [unclosed\n"), kind.YAML, "bad").Crumbs(1, 0); got != nil {
		t.Errorf("malformed YAML = %v, want no crumbs", got)
	}
}

func TestTextHasOnlyItsTitle(t *testing.T) {
	d := Read([]byte("# looks like a heading\nplain\n"), kind.Text, "notes")
	if len(d.Headings()) != 0 {
		t.Fatalf("plain text headings: %v", d.Headings())
	}
	if got := d.Crumbs(2, 0); !reflect.DeepEqual(got, []string{"notes"}) {
		t.Fatalf("crumbs = %v", got)
	}
}
