package schema

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const kbSchema = `version: 1
frontmatter:
  type:
    type: string
    enum: [note, adr, review]
    required: true
  status:
    type: string
    enum: [draft, active, archived]
    default: active
  title:
    type: string
  tags:
    type: list
    item_type: string
  created:
    type: date
  count:
    type: integer
  done:
    type: boolean
`

func mustParse(t *testing.T, src string) *Schema {
	t.Helper()
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return s
}

func TestParseReadsFieldsInOrder(t *testing.T) {
	s := mustParse(t, kbSchema)
	var names []string
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	if want := []string{"type", "status", "title", "tags", "created", "count", "done"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("fields = %v, want %v", names, want)
	}
	typ, _ := s.Field("type")
	if !typ.Required || !reflect.DeepEqual(typ.Enum, []string{"note", "adr", "review"}) || typ.Line != 3 {
		t.Fatalf("type = %+v", typ)
	}
	status, _ := s.Field("status")
	if !reflect.DeepEqual(status.Default, []string{"active"}) {
		t.Fatalf("status default = %v", status.Default)
	}
	tags, _ := s.Field("tags")
	if tags.Type != List || tags.ItemType != String {
		t.Fatalf("tags = %+v", tags)
	}
}

// A schema error names the line it is on, so the manager can point at it.
func TestParseErrorsAreLineAware(t *testing.T) {
	cases := []struct {
		name, src string
		line      int
		msg       string
	}{
		{"malformed yaml", "version: 1\nfrontmatter:\n  a: [unclosed\n", 4, ""}, // where the parser finds the end
		{"no version", "frontmatter: {}\n", 1, "version"},
		{"wrong version", "version: 2\nfrontmatter: {}\n", 1, "version must be 1"},
		{"unknown top key", "version: 1\nfields: {}\n", 2, `unknown key "fields"`},
		{"unknown type", "version: 1\nfrontmatter:\n  a:\n    type: text\n", 4, "type must be one of"},
		{"list item list", "version: 1\nfrontmatter:\n  a: {type: list, item_type: list}\n", 3, "item_type"},
		{"enum of wrong type", "version: 1\nfrontmatter:\n  n:\n    type: integer\n    enum: [1, two]\n", 5, "not a integer"},
		{"default of wrong type", "version: 1\nfrontmatter:\n  n:\n    type: integer\n    default: x\n", 5, "default is a string"},
		{"default outside enum", "version: 1\nfrontmatter:\n  s:\n    type: string\n    enum: [a]\n    default: b\n", 6, "not one of its enum"},
		{"required with default", "version: 1\nfrontmatter:\n  s:\n    type: string\n    required: true\n    default: a\n", 6, "required field has no default"},
		{"no type", "version: 1\nfrontmatter:\n  s: {required: true}\n", 3, "needs a type"},
		{"duplicate field", "version: 1\nfrontmatter:\n  s: {type: string}\n  s: {type: string}\n", 4, ""},
		{"not a mapping", "- 1\n", 1, "must be a mapping"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.src))
			var se *Error
			if !errors.As(err, &se) {
				t.Fatalf("err = %v, want a *schema.Error", err)
			}
			if se.Line != c.line {
				t.Errorf("line = %d, want %d (%v)", se.Line, c.line, err)
			}
			if !strings.Contains(se.Msg, c.msg) {
				t.Errorf("msg = %q, want it to contain %q", se.Msg, c.msg)
			}
		})
	}
}

func TestParseRefusesOversizedSchema(t *testing.T) {
	_, err := Parse([]byte("version: 1\n" + strings.Repeat("#", MaxSize)))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestEmptyFrontmatterDeclaresNoFields(t *testing.T) {
	s := mustParse(t, "version: 1\nfrontmatter:\n")
	if len(s.Fields) != 0 {
		t.Fatalf("fields = %v", s.Fields)
	}
}

func note(fm string) []byte { return []byte("---\n" + fm + "---\n# Body\n\ntext\n") }

func rules(r Result) []string {
	var out []string
	for _, d := range r.Diagnostics {
		out = append(out, d.Field+":"+d.Rule)
	}
	return out
}

func TestValidateAdmitsValidFieldsAndDefaults(t *testing.T) {
	s := mustParse(t, kbSchema)
	r := s.ValidateNote(note("type: adr\ntitle: Storage\ntags: [store, sqlite]\ncreated: 2026-10-02T12:38:27+09:00\ncount: 0x10\ndone: true\n"))
	if len(r.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}
	want := []Facet{{"type", "adr"}, {"title", "Storage"}, {"tags", "store"}, {"tags", "sqlite"},
		{"created", "2026-10-02"}, {"count", "16"}, {"done", "true"}, {"status", "active"}}
	if !reflect.DeepEqual(r.Facets, want) {
		t.Fatalf("facets = %v\nwant     %v", r.Facets, want)
	}
}

// An invalid field is reported on its own line and kept out of the facets; the other fields stay.
func TestValidateReportsEachRuleOnItsLine(t *testing.T) {
	s := mustParse(t, kbSchema)
	src := note("type: memo\nstatus: active\ncount: many\ntags: [a, 3]\ncreated: yesterday\n")
	r := s.ValidateNote(src)
	want := []Diagnostic{
		{Field: "type", Line: 2, Rule: RuleEnum},
		{Field: "count", Line: 4, Rule: RuleType},
		{Field: "tags", Line: 5, Rule: RuleType},
		{Field: "created", Line: 6, Rule: RuleType},
	}
	if len(r.Diagnostics) != len(want) {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}
	for i, d := range r.Diagnostics {
		if d.Field != want[i].Field || d.Line != want[i].Line || d.Rule != want[i].Rule || d.Message == "" {
			t.Errorf("diagnostic %d = %+v, want %+v", i, d, want[i])
		}
	}
	if !reflect.DeepEqual(r.Facets, []Facet{{"status", "active"}}) {
		t.Fatalf("facets = %v, want only the valid status", r.Facets)
	}
}

func TestValidateMissingRequiredAndEmptyValue(t *testing.T) {
	s := mustParse(t, kbSchema)
	for name, src := range map[string][]byte{
		"absent":         note("title: x\n"),
		"empty value":    note("type:\ntitle: x\n"),
		"no frontmatter": []byte("# Just a note\n"),
	} {
		r := s.ValidateNote(src)
		if got := rules(r); !reflect.DeepEqual(got, []string{"type:required"}) {
			t.Errorf("%s: diagnostics = %v", name, got)
		}
		if r.Diagnostics[0].Line != 1 {
			t.Errorf("%s: line = %d, want 1", name, r.Diagnostics[0].Line)
		}
	}
}

// A lone scalar where a list is declared is a list of one, as Obsidian writes tags.
func TestValidateScalarIsAListOfOne(t *testing.T) {
	s := mustParse(t, kbSchema)
	r := s.ValidateNote(note("type: note\ntags: solo\n"))
	if len(r.Diagnostics) != 0 || !reflect.DeepEqual(r.Facets[1], Facet{"tags", "solo"}) {
		t.Fatalf("result = %+v", r)
	}
}

func TestValidateStrictReportsUnknownFields(t *testing.T) {
	loose := mustParse(t, kbSchema)
	strict := mustParse(t, "strict: true\n"+kbSchema)
	src := note("type: note\nstauts: open\n")
	if got := rules(loose.ValidateNote(src)); len(got) != 0 {
		t.Fatalf("loose: %v", got)
	}
	r := strict.ValidateNote(src)
	if got := rules(r); !reflect.DeepEqual(got, []string{"stauts:unknown"}) || r.Diagnostics[0].Line != 3 {
		t.Fatalf("strict: %+v", r.Diagnostics)
	}
}

// Malformed YAML is reported (on the note's line) even with no schema, and nothing is faceted.
func TestValidateMalformedYAML(t *testing.T) {
	for _, s := range []*Schema{nil, mustParse(t, kbSchema)} {
		r := s.ValidateNote(note("type: note\ntags: [a,\n"))
		if len(r.Diagnostics) != 1 || r.Diagnostics[0].Rule != RuleYAML || r.Diagnostics[0].Line < 2 {
			t.Fatalf("schema %v: %+v", s != nil, r.Diagnostics)
		}
		if len(r.Facets) != 0 {
			t.Fatalf("facets from malformed YAML: %v", r.Facets)
		}
	}
	r := (*Schema)(nil).ValidateNote(note("a: 1\na: 2\n"))
	if got := rules(r); !reflect.DeepEqual(got, []string{":yaml"}) {
		t.Fatalf("duplicate key: %v", got)
	}
	r = (*Schema)(nil).ValidateNote(note("- a\n- b\n"))
	if got := rules(r); !reflect.DeepEqual(got, []string{":mapping"}) {
		t.Fatalf("a list: %v", got)
	}
}

func TestNilSchemaFacetsNothing(t *testing.T) {
	r := (*Schema)(nil).ValidateNote(note("type: note\n"))
	if len(r.Facets) != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("result = %+v", r)
	}
}

func TestFacetValueReadsTypedText(t *testing.T) {
	s := mustParse(t, kbSchema)
	cases := []struct{ field, text, want string }{
		{"count", "0x10", "16"},
		{"count", "7", "7"},
		{"done", "true", "true"},
		{"created", "2026-10-02", "2026-10-02"},
		{"status", "active", "active"},
		{"title", "007", "007"}, // a string field takes the text as typed
		{"tags", "store", "store"},
	}
	for _, c := range cases {
		got, err := s.FacetValue(c.field, c.text)
		if err != nil || got != c.want {
			t.Errorf("FacetValue(%s, %s) = %q, %v; want %q", c.field, c.text, got, err, c.want)
		}
	}
	for _, bad := range [][2]string{{"count", "many"}, {"done", "maybe"}, {"created", "today"}, {"nope", "x"}} {
		if _, err := s.FacetValue(bad[0], bad[1]); err == nil {
			t.Errorf("FacetValue(%s, %s) accepted", bad[0], bad[1])
		}
	}
}
