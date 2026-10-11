package todo

import (
	"fmt"
	"strings"
	"testing"
)

// show is a decoded node, compactly: "x" a string, n:1 a number, b:true, null, [..] a list,
// {k: v, ..} a mapping in file order.
func show(n *node) string {
	switch n.kind {
	case nString:
		return fmt.Sprintf("%q", n.str)
	case nNumber:
		return "n:" + n.str
	case nBool:
		return fmt.Sprintf("b:%v", n.b)
	case nList:
		var items []string
		for _, it := range n.items {
			items = append(items, show(it))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case nMap:
		var kv []string
		for i, k := range n.keys {
			kv = append(kv, k+": "+show(n.vals[i]))
		}
		return "{" + strings.Join(kv, ", ") + "}"
	}
	return "null"
}

// The decoder's accepts and refusals, case by case. want is the decoded mapping, or "!" and the
// text the refusal must contain. lua notes where auto-core's yaml.lua (tinyyaml, v0.3.2, run under
// luajit) answers otherwise, and why AutoDoc differs on purpose: every such difference refuses, or
// reads correctly, what tinyyaml reads silently wrong.
func TestDecodeYAML(t *testing.T) {
	cases := []struct {
		name, src, want, lua string
	}{
		// what the strict subset refuses outside block scalars, as yaml.lua does
		{"anchor", "a: &x 1", "!YAML anchor `&x`", ""},
		{"alias", "a: *x", "!YAML alias `*x`", ""},
		{"anchor in a list", "a:\n  - &x v", "!YAML anchor `&x`", ""},
		{"merge key", "<<: {b: 1}", "!merge key `<<:`", ""},
		{"merge key in a list", "a:\n  - <<: b", "!merge key `<<:`", ""},
		{"explicit tag", "a: !!str x", "!YAML explicit tag `!!str`", ""},
		{"local tag", "a: !local x", "!anchors, aliases and tags are not supported", `drops the tag: "x"`},
		{"anchor after a comment is a comment", "a: x # &y", `{a: "x"}`, ""},
		{"anchor text in a block scalar", "a: |\n  &x text\n  *y too\n", `{a: "&x text\n*y too\n"}`, ""},
		{"duplicate key", "a: 1\na: 2", `!duplicate key "a"`, `renames it a_1, which the closed schema then refuses`},
		{"tab in indentation", "a:\n\t- x", "!tab in indentation", `reads a tab as indent: [x]`},
		{"tab in a list's indentation", "a:\n  - x\n\t- y", "!tab in indentation", ""},
		{"tab before a nested item", "a:\n  -\n\t- y", "!tab in indentation", ""},

		// flow collections: one line, scalars only; items stay strings, as tinyyaml leaves them
		{"flow list", `a: [x, "y z", 'q', ]`, `{a: ["x", "y z", "q"]}`, ""},
		{"empty flow list", "a: [] # none", `{a: []}`, ""},
		{"ints in a flow list are text", "a: [1, 2]", `{a: ["1", "2"]}`, ""},
		{"nested flow list", "a: [x, [y]]", "!nested flow collections", `reads [x, [y]]`},
		{"unterminated flow list", "a: [x, y", "!unterminated flow sequence", `reads [x]`},
		{"unterminated flow list after a quote", `a: ["x"`, "!bad flow sequence", ""},
		{"unterminated quote in a flow list", `a: ["x, y]`, "!unterminated quoted scalar", ""},
		{"text after a flow list", "a: [x] y", "!unexpected text after a flow sequence", ""},
		{"no separator in a flow list", `a: ["x" "y"]`, "!bad flow sequence", ""},
		{"empty flow map", "a: {} # none", `{a: {}}`, ""},
		{"flow map", "a: {b: 1}", "!flow mappings are not supported", `reads {b = "1"}`},

		// block scalars: chomping, indentation indicators, folding
		{"literal", "a: |\n  l1\n  l2\nb: 1", `{a: "l1\nl2\n", b: n:1}`, ""},
		{"literal keeps an inner blank line", "a: |\n  l1\n\n  l2\n", `{a: "l1\n\nl2\n"}`, `drops it: "l1\nl2\n"`},
		{"literal keeps a # line", "a: |\n  # h\n  x\n", `{a: "# h\nx\n"}`, `drops it: "x\n"`},
		{"strip", "a: |-\n  l1\n\n", `{a: "l1"}`, ""},
		{"keep", "a: |+\n  l1\n\n\nb: 1", `{a: "l1\n\n\n", b: n:1}`, `drops blank lines first: "l1\n"`},
		{"folded", "a: >\n  w1\n  w2\n\n  w3\n", `{a: "w1 w2\nw3\n"}`, `joins every line: "w1 w2 w3\n"`},
		{"folded, stripped", "a: >-\n  w1\n  w2\n", `{a: "w1 w2"}`, ""},
		{"indentation indicator", "a: |2\n    x\n  y\n", `{a: "  x\ny\n"}`, "a parser error"},
		{"indicator and chomping", "a: |-2\n    x\n", `{a: "  x"}`, ""},
		{"blank line under an indicator", "a: |1\n x\n   \n y\n", `{a: "x\n  \ny\n"}`, ""},
		{"header comment", "a: | # note\n  x\n", `{a: "x\n"}`, ""},
		{"empty block", "a: |\nb: 1", `{a: "", b: n:1}`, ""},
		{"empty kept block", "a: |+\n\n\nb: 1", `{a: "\n\n", b: n:1}`, ""},
		{"block at the end", "a: |", `{a: ""}`, ""},
		{"block in a list", "a:\n  - |\n    x\n  - y", `{a: ["x\n", "y"]}`, ""},
		{"less indented block line", "a: |\n    deeper\n  less\n", "!bad indentation in a block scalar", ""},
		{"bad block header", "a: |x", "!bad block scalar header", ""},
		{"two chomping indicators", "a: |-+", "!bad block scalar header", ""},

		// quoted scalars and escapes
		{"escapes", `a: "t\tn\nr\rq\"s\\/\/0\0a\ab\bv\ve\ef\f sp\ N\N _\_ L\L P\P tab\	x"`,
			fmt.Sprintf("{a: %q}", "t\tn\nr\rq\"s\\//0\x00a\ab\bv\ve\x1bf\f sp N\u0085 _\u00a0 L\u2028 P\u2029 tab\tx"), ""},
		{"hex and unicode escapes", `a: "\x41\u00e9\U0001F600"`, `{a: "Aé😀"}`, `reads "x41u00e9U0001F600"`},
		{"unknown escape", `a: "\q"`, `{a: "q"}`, ""},
		{"short escape", `a: "\x4`, "!short escape", ""},
		{"bad hex escape", `a: "\xZZ"`, "!bad escape", ""},
		{"escape past unicode", `a: "\UFFFFFFFF"`, "!bad escape", ""},
		{"single quotes", "a: 'it''s'", `{a: "it's"}`, `stops at the first quote: "it"`},
		{"unterminated quote", `a: "open`, "!unterminated quoted scalar", `reads "ope"`},
		{"text after a quote", `a: "x" y`, "!unexpected text after a quoted scalar", `reads "x"`},
		{"comment after a quote", `a: "x" # c`, `{a: "x"}`, ""},
		{"quoted keys", "\"a b\": 1\n'c': d", `{a b: n:1, c: "d"}`, ""},
		{"quoted key, no colon", `"k" x`, "!failed to classify line", ""},
		{"quoted key, colon run on", `"k":v`, "!failed to classify line", ""},
		{"unterminated quoted key", `"k: v`, "!unterminated quoted scalar", ""},

		// null, booleans and numbers, typed as tinyyaml types them
		{"nulls", "a:\nb: ~\nc: null\nd: Null\ne: NULL", `{a: null, b: null, c: null, d: null, e: null}`, ""},
		{"booleans", "a: true\nb: True\nc: FALSE", `{a: b:true, b: b:true, c: b:false}`, ""},
		{"numbers", "a: 1\nb: -2\nc: +3\nd: 1.\ne: 1.5\nf: .inf\ng: -.Inf\nh: .nan", `{a: n:1, b: n:-2, c: n:+3, d: n:1., e: n:1.5, f: n:.inf, g: n:-.Inf, h: n:.nan}`, ""},
		{"text that looks typed", "a: 1e3\nb: 0x1F\nc: yes\nd: 2026-01-01\ne: 2026-01-01T09:00:00+09:00",
			`{a: "1e3", b: "0x1F", c: "yes", d: "2026-01-01", e: "2026-01-01T09:00:00+09:00"}`, ""},

		// plain scalars
		{"comment after a plain scalar", "a: x #y\nb: x#y\nc: C:\\path", `{a: "x", b: "x#y", c: "C:\\path"}`, ""},
		{"a mapping value in a plain scalar", "a: b: c", "!a mapping value is not allowed here", `truncates it: "b"`},
		{"a trailing colon", "a: note:", "!a mapping value is not allowed here", `truncates it: "note"`},
		{"a key with no space", "a:1", "!failed to classify line", ""},
		{"no key", ": x", "!failed to classify line", ""},

		// structure
		{"empty", "", "!frontmatter is empty", "a parser error"},
		{"only comments", "# c\n\n", "!frontmatter is empty", ""},
		{"a list at the root", "- a", "!frontmatter is not a mapping", `reads [a]`},
		{"over-indented key", "a: 1\n  b: 2", "!bad indentation", ""},
		{"a dedent after an indented root", "  a: 1\nb: 2", "!bad indentation", ""},
		{"tab before the first key", "\ta: 1", "!tab in indentation", ""},
		{"nested mapping", "a:\n  b: 1\n  # c\n  c: 2\nd: 3", `{a: {b: n:1, c: n:2}, d: n:3}`, ""},
		{"over-indented nested key", "a:\n  b: 1\n    c: 2", "!bad indentation", ""},
		{"a list at the key's indent", "a:\n- x\n- y\nb: 1", `{a: ["x", "y"], b: n:1}`, ""},
		{"empty items", "a:\n  - x\n  -\n  - y\n  - # c", `{a: ["x", null, "y", null]}`, ""},
		{"nested lists", "a:\n  - - x\n    - y\n  -\n    - z", `{a: [["x", "y"], ["z"]]}`, ""},
		{"a mapping under an empty item", "a:\n  -\n    k: v", `{a: [{k: "v"}]}`, ""},
		{"mapping items", "a:\n  - k: v\n    j: w\n  - k: u", `{a: [{k: "v", j: "w"}, {k: "u"}]}`, ""},
		{"an item that is not a key", "a:\n  - https://x", "!failed to classify line", ""},
		{"an over-indented item", "a:\n  - x\n    - y", "!bad indentation", ""},
		{"a bad line inside an item's mapping", "a:\n  - k: v\n    oops", "!failed to classify line", ""},
		{"an error inside a nested item", "a:\n  -\n    - [x", "!unterminated flow sequence", ""},
		{"an error inside a nested mapping", "a:\n  b: [x", "!unterminated flow sequence", ""},
		{"an error inside a nested list", "a:\n  - - [x", "!unterminated flow sequence", ""},
		{"a null at the end", "a:\n# c", `{a: null}`, ""},
		{"CRLF", "a: 1\r\nb: 2\r\n", `{a: n:1, b: n:2}`, ""},
	}
	for _, c := range cases {
		n, err := decodeYAML(c.src)
		switch {
		case strings.HasPrefix(c.want, "!"):
			if err == nil || !strings.Contains(err.Error(), c.want[1:]) {
				got := "accepted"
				if n != nil {
					got = show(n)
				}
				t.Errorf("%s: %v (%s), want a refusal %q", c.name, err, got, c.want[1:])
			}
		case err != nil:
			t.Errorf("%s: %v, want %s", c.name, err, c.want)
		case show(n) != c.want:
			t.Errorf("%s:\n got %s\nwant %s", c.name, show(n), c.want)
		}
	}
}

// Where AutoDoc's decoding differs from auto-core's on purpose, at the level of a whole task: the
// schema is core/schema's, which types values by YAML 1.2's Core schema.
func TestDecodeTaskTyping(t *testing.T) {
	head := "---\nid: a\nstatus: open\ntitle: t\ncreated: \"2026-01-01T00:00:00Z\"\nupdated: \"2026-01-01T00:00:00Z\"\n" +
		"status_changed: \"2026-01-01T00:00:00Z\"\n"
	cases := []struct {
		name, fm, want, lua string
	}{
		{"version 1", "version: 1", "", ""},
		{"version 1.0", "version: 1.0", "!version is a number", "accepts it: 1.0 == 1 in Lua"},
		{"version as text", `version: "1"`, "!version must be a whole number", ""},
		{"version 2", "version: 2", "!format version 2 is newer than this AutoDoc", ""},
		{"no version", "", "!version is required", ""},
		{"ints in a flow list", "version: 1\ntags: [1, 2]", "!tags item 1 is an integer", "accepts them as text"},
		{"an empty tag", "version: 1\ntags: \"\"", "", "refuses it (an empty string is no list)"},
		{"a boolean title", "version: 1\ntitle2: x", "!title2", ""},
		{"a list where a string goes", "version: 1\nassignee: [x]", "!assignee is a list, not a string", ""},
		{"a mapping where a list goes", "version: 1\ntags:\n  k: v", "!tags", ""},
		{"null fields are absent", "version: 1\nassignee:\ndue: ~\npriority: null\ntags:", "", "refuses them: tinyyaml's null is a table"},
		{"a frontmatter description is ignored", "version: 1\ndescription: in fm", "", ""},
	}
	for _, c := range cases {
		src := strings.Replace(head, "status: open\n", "status: open\n"+c.fm+"\n", 1) + "---\n\nbody\n"
		task, err := decode([]byte(src))
		switch {
		case strings.HasPrefix(c.want, "!"):
			if err == nil || !strings.Contains(err.Error(), c.want[1:]) {
				t.Errorf("%s: %v, want a refusal %q", c.name, err, c.want[1:])
			}
		case err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.name == "a frontmatter description is ignored" && task.Description != "body":
			t.Errorf("%s: description %q", c.name, task.Description)
		}
	}
}

func TestKindNames(t *testing.T) {
	for kind, want := range map[nodeKind]string{nString: "string", nBool: "boolean", nNumber: "number", nList: "table", nMap: "table", nNull: "nil"} {
		if got := (&node{kind: kind}).kindName(); got != want {
			t.Errorf("%v: %s", kind, got)
		}
	}
}
