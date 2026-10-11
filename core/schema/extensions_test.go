package schema

import (
	"reflect"
	"strings"
	"testing"
)

const itemsSchema = `version: 1
strict: true
order: [id, status, errors]
frontmatter:
  id: {type: string, required: true, managed: true}
  status: {type: string, enum: [open, done], required: true}
  title: {type: string}
  errors:
    type: list
    item_type: map
    managed: true
    fields:
      code: {type: string, enum: [not-found, unresolved], required: true}
      message: {type: string, required: true}
      tags: {type: list}
`

func TestManagedIsDeclared(t *testing.T) {
	s := mustParse(t, itemsSchema)
	for name, want := range map[string]bool{"id": true, "status": false, "title": false, "errors": true} {
		if f, _ := s.Field(name); f.Managed != want {
			t.Errorf("%s: Managed = %v, want %v", name, f.Managed, want)
		}
	}
	if _, err := Parse([]byte("version: 1\nfrontmatter:\n  a: {type: string, managed: yes}\n")); err == nil ||
		!strings.Contains(err.Error(), "managed must be true or false") {
		t.Errorf("managed: yes: %v", err)
	}
	// a version 2 type may make a common field managed, never unmanage it
	s = mustParse(t, "version: 2\ndiscriminator: type\ncommon:\n  type: {type: string}\n  id: {type: string, managed: true}\n"+
		"  note: {type: string}\ntypes:\n  adr:\n    note: {type: string, managed: true}\n")
	if f, _ := s.types["adr"].field("note"); !f.Managed {
		t.Error("a type's managed was lost in the merge")
	}
	if f, _ := s.types["adr"].field("id"); !f.Managed {
		t.Error("common's managed was lost in the merge")
	}
}

func TestOrderIsTheSchemas(t *testing.T) {
	s := mustParse(t, itemsSchema)
	if got := s.Order(); !reflect.DeepEqual(got, []string{"id", "status", "errors"}) {
		t.Fatalf("Order = %v", got)
	}
	s.Order()[0] = "changed"
	if s.Order()[0] != "id" {
		t.Error("Order handed out its own slice")
	}
	if got := mustParse(t, kbSchema).Order(); got != nil {
		t.Errorf("no order list: Order = %v, want nil", got)
	}
	var none *Schema
	if none.Order() != nil {
		t.Error("a nil schema has an order")
	}
	v2 := mustParse(t, "version: 2\ndiscriminator: type\norder: [type, verdict]\ncommon:\n  type: {type: string}\n"+
		"types:\n  review:\n    verdict: {type: string}\n")
	if got := v2.Order(); !reflect.DeepEqual(got, []string{"type", "verdict"}) {
		t.Errorf("version 2: Order = %v (a type's field is declared)", got)
	}
}

func TestOrderErrors(t *testing.T) {
	cases := []struct{ name, src, msg string }{
		{"undeclared", "version: 1\norder: [a, b]\nfrontmatter:\n  a: {type: string}\n", `order: "b" is not a declared field`},
		{"twice", "version: 1\norder: [a, a]\nfrontmatter:\n  a: {type: string}\n", `order: "a" is named twice`},
		{"not a list", "version: 1\norder: a\nfrontmatter:\n  a: {type: string}\n", "order must be a list"},
		{"not a name", "version: 1\norder: [1]\nfrontmatter:\n  a: {type: string}\n", "a field name must be a string"},
		{"v2 undeclared", "version: 2\ndiscriminator: type\norder: [nope]\ncommon:\n  type: {type: string}\n", `"nope" is not a declared field`},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.src))
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.msg)
		}
		if se, ok := err.(*Error); ok && se.Line < 2 {
			t.Errorf("%s: line %d, want the order's line", c.name, se.Line)
		}
	}
}

func TestMapItemsAreDeclared(t *testing.T) {
	s := mustParse(t, itemsSchema)
	f, _ := s.Field("errors")
	if f.Type != List || f.ItemType != Map || len(f.Items) != 3 {
		t.Fatalf("errors = %+v", f)
	}
	code := f.Items[0]
	if code.Name != "code" || !code.Required || !reflect.DeepEqual(code.Enum, []string{"not-found", "unresolved"}) {
		t.Errorf("code = %+v", code)
	}
	if tags := f.Items[2]; tags.Type != List || tags.ItemType != String {
		t.Errorf("an item's list field = %+v", tags)
	}
}

func TestMapItemsParseErrors(t *testing.T) {
	head := "version: 1\nfrontmatter:\n  e:\n"
	cases := []struct{ name, decl, msg string }{
		{"nested map items", "    type: list\n    item_type: map\n    fields:\n      inner: {type: list, item_type: map, fields: {a: {type: string}}}\n",
			"a map item holds no further map items"},
		{"item default", "    type: list\n    item_type: map\n    fields:\n      a: {type: string, default: x}\n", "an item's field has no default"},
		{"no fields", "    type: list\n    item_type: map\n", "item_type: map needs fields"},
		{"empty fields", "    type: list\n    item_type: map\n    fields: {}\n", "declares no fields"},
		{"fields without map", "    type: list\n    fields: {a: {type: string}}\n", "for type: list with item_type: map"},
		{"enum on the list", "    type: list\n    item_type: map\n    enum: [a]\n    fields: {a: {type: string}}\n", "has no enum"},
		{"default on the list", "    type: list\n    item_type: map\n    default: []\n    fields: {a: {type: string}}\n", "has no default"},
		{"map as a type", "    type: map\n", "type must be one of"},
		{"unknown item key", "    type: list\n    item_type: map\n    fields:\n      a: {type: string, colour: red}\n", `unknown key "colour"`},
	}
	for _, c := range cases {
		_, err := Parse([]byte(head + c.decl))
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.msg)
		}
	}
	// a version 2 type cannot redeclare common's map items differently
	_, err := Parse([]byte("version: 2\ndiscriminator: type\ncommon:\n  type: {type: string}\n" +
		"  e: {type: list, item_type: map, fields: {a: {type: string}}}\ntypes:\n  x:\n" +
		"    e: {type: list, item_type: map, fields: {a: {type: integer}}}\n"))
	if err == nil || !strings.Contains(err.Error(), "a field has one type") {
		t.Errorf("v2 redeclared items: %v", err)
	}
}

func TestValidateMapItems(t *testing.T) {
	s := mustParse(t, itemsSchema)
	ok := "id: a\nstatus: open\nerrors:\n  - code: not-found\n    message: gone\n    tags: [x]\n  - code: unresolved\n    message: \"$X\"\n"
	if r := s.Validate([]byte(ok), true); len(r.Diagnostics) != 0 {
		t.Fatalf("valid items: %+v", r.Diagnostics)
	}
	cases := []struct{ name, fm, rule, msg string }{
		{"unknown item key under strict", "id: a\nstatus: open\nerrors:\n  - code: not-found\n    message: m\n    colour: red\n",
			RuleUnknown, `errors item 1: "colour" is not a field of its items`},
		{"item not a mapping", "id: a\nstatus: open\nerrors:\n  - just text\n", RuleType, "errors item 1 is a string, not a mapping"},
		{"item field of the wrong type", "id: a\nstatus: open\nerrors:\n  - code: not-found\n    message: [a, b]\n",
			RuleType, "errors item 1: message is a list, not a string"},
		{"item field outside its enum", "id: a\nstatus: open\nerrors:\n  - code: lost\n    message: m\n", RuleEnum, `errors item 1: code: "lost" is not one of`},
		{"item field missing", "id: a\nstatus: open\nerrors:\n  - code: not-found\n  - message: m\n    code: not-found\n",
			RuleRequired, "errors item 1: message is required"},
		{"a lone mapping", "id: a\nstatus: open\nerrors:\n  code: not-found\n  message: m\n", RuleType, "errors is a mapping, not a list of mappings"},
	}
	for _, c := range cases {
		r := s.Validate([]byte(c.fm), true)
		if len(r.Diagnostics) != 1 || r.Diagnostics[0].Rule != c.rule || !strings.Contains(r.Diagnostics[0].Message, c.msg) ||
			r.Diagnostics[0].Field != "errors" {
			t.Errorf("%s: %+v, want one %s %q", c.name, r.Diagnostics, c.rule, c.msg)
		}
	}
	// without strict, an unknown item key is let through, as an unknown field is
	loose := mustParse(t, strings.Replace(itemsSchema, "strict: true", "strict: false", 1))
	if r := loose.Validate([]byte("id: a\nstatus: open\nerrors:\n  - code: not-found\n    message: m\n    colour: red\n"), true); len(r.Diagnostics) != 0 {
		t.Errorf("not strict: %+v", r.Diagnostics)
	}
	// a diagnostic names the item's own line
	r := s.Validate([]byte("id: a\nstatus: open\nerrors:\n  - code: not-found\n    message: m\n  - code: lost\n    message: m\n"), true)
	if len(r.Diagnostics) != 1 || r.Diagnostics[0].Line != 7 {
		t.Errorf("line: %+v, want 7 (the frontmatter starts on line 2)", r.Diagnostics)
	}
	// a map item gives no facets
	for _, f := range s.Validate([]byte(ok), true).Facets {
		if f.Field == "errors" {
			t.Errorf("a map item gave a facet: %+v", f)
		}
	}
}
