package schema

import (
	"errors"
	"fmt"
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
		{"wrong version", "version: 3\nfrontmatter: {}\n", 1, "version must be 1 or 2"},
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
	r := s.ValidateFile(note("type: adr\ntitle: Storage\ntags: [store, sqlite]\ncreated: 2026-10-02T12:38:27+09:00\ncount: 0x10\ndone: true\n"))
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
	r := s.ValidateFile(src)
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
		r := s.ValidateFile(src)
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
	r := s.ValidateFile(note("type: note\ntags: solo\n"))
	if len(r.Diagnostics) != 0 || !reflect.DeepEqual(r.Facets[1], Facet{"tags", "solo"}) {
		t.Fatalf("result = %+v", r)
	}
}

func TestValidateStrictReportsUnknownFields(t *testing.T) {
	loose := mustParse(t, kbSchema)
	strict := mustParse(t, "strict: true\n"+kbSchema)
	src := note("type: note\nstauts: open\n")
	if got := rules(loose.ValidateFile(src)); len(got) != 0 {
		t.Fatalf("loose: %v", got)
	}
	r := strict.ValidateFile(src)
	if got := rules(r); !reflect.DeepEqual(got, []string{"stauts:unknown"}) || r.Diagnostics[0].Line != 3 {
		t.Fatalf("strict: %+v", r.Diagnostics)
	}
}

// Malformed YAML is reported (on the file's line) even with no schema, and nothing is faceted.
func TestValidateMalformedYAML(t *testing.T) {
	for _, s := range []*Schema{nil, mustParse(t, kbSchema)} {
		r := s.ValidateFile(note("type: note\ntags: [a,\n"))
		if len(r.Diagnostics) != 1 || r.Diagnostics[0].Rule != RuleYAML || r.Diagnostics[0].Line < 2 {
			t.Fatalf("schema %v: %+v", s != nil, r.Diagnostics)
		}
		if len(r.Facets) != 0 {
			t.Fatalf("facets from malformed YAML: %v", r.Facets)
		}
	}
	r := (*Schema)(nil).ValidateFile(note("a: 1\na: 2\n"))
	if got := rules(r); !reflect.DeepEqual(got, []string{":yaml"}) {
		t.Fatalf("duplicate key: %v", got)
	}
	r = (*Schema)(nil).ValidateFile(note("- a\n- b\n"))
	if got := rules(r); !reflect.DeepEqual(got, []string{":mapping"}) {
		t.Fatalf("a list: %v", got)
	}
}

func TestNilSchemaFacetsNothing(t *testing.T) {
	r := (*Schema)(nil).ValidateFile(note("type: note\n"))
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

const kbSchemaV2 = `version: 2
discriminator: type
common:
  type:
    type: string
    required: true
  title:
    type: string
  status:
    type: string
    enum: [draft, proposed, accepted, approved, rejected]
    default: draft
  tags:
    type: list
types:
  adr:
    number:
      type: string
      required: true
    status:
      type: string
      enum: [proposed, accepted]
      default: proposed
  review:
    reviewer:
      type: string
      required: true
    verdict:
      type: string
      enum: [approved, rejected]
      required: true
    status:
      type: string
      required: true
  note:
`

// A version 2 schema's Fields are the union of common and the types, each once, in the schema's
// order: what the indexer facets and the query filters by.
func TestParseV2FieldsAreTheUnion(t *testing.T) {
	s := mustParse(t, kbSchemaV2)
	if s.Version != 2 || s.Discriminator != "type" {
		t.Fatalf("version %d, discriminator %q", s.Version, s.Discriminator)
	}
	var names []string
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	if want := []string{"type", "title", "status", "tags", "number", "reviewer", "verdict"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("fields = %v, want %v", names, want)
	}
	status, _ := s.Field("status")
	if status.Line != 9 || len(status.Enum) != 5 {
		t.Fatalf("status = %+v, want common's declaration", status)
	}
	for field, text := range map[string]string{"verdict": "approved", "number": "7", "type": "adr"} {
		if v, err := s.FacetValue(field, text); err != nil || v != text {
			t.Errorf("FacetValue(%s, %s) = %q, %v", field, text, v, err)
		}
	}
	if v1 := mustParse(t, kbSchema); v1.Version != 1 || v1.Discriminator != "" {
		t.Fatalf("a version 1 schema reads as version %d, discriminator %q", v1.Version, v1.Discriminator)
	}
}

// A document is checked against common merged with its type: a type adds fields, narrows a common
// field's enum, makes it required or gives it its own default; a type the schema does not name is
// diagnosed and checked against common alone.
func TestValidateV2ByType(t *testing.T) {
	s := mustParse(t, kbSchemaV2)
	cases := []struct {
		name, fm string
		rules    []string
		facets   []Facet
	}{
		{"review without verdict", "type: review\nreviewer: kim\nstatus: draft\n", []string{"verdict:required"},
			[]Facet{{"type", "review"}, {"reviewer", "kim"}, {"status", "draft"}}},
		{"adr without verdict", "type: adr\nnumber: '7'\n", nil,
			[]Facet{{"type", "adr"}, {"number", "7"}, {"status", "proposed"}}}, // the adr's own default
		{"review with every field", "type: review\nreviewer: kim\nverdict: approved\nstatus: approved\n", nil,
			[]Facet{{"type", "review"}, {"reviewer", "kim"}, {"verdict", "approved"}, {"status", "approved"}}},
		{"narrowed enum", "type: adr\nnumber: '7'\nstatus: approved\n", []string{"status:enum"},
			[]Facet{{"type", "adr"}, {"number", "7"}}},
		{"common's enum where the type has none", "type: note\nstatus: approved\n", nil,
			[]Facet{{"type", "note"}, {"status", "approved"}}},
		{"required in the type", "type: review\nreviewer: kim\nverdict: rejected\n", []string{"status:required"},
			[]Facet{{"type", "review"}, {"reviewer", "kim"}, {"verdict", "rejected"}}},
		{"common's default", "type: note\n", nil, []Facet{{"type", "note"}, {"status", "draft"}}},
		{"unknown type", "type: memo\nstatus: bogus\n", []string{"type:unknown_type", "status:enum"},
			[]Facet{{"type", "memo"}}}, // common alone: no number or verdict required, common's enum checked
		{"no type", "title: x\n", []string{"type:required"}, []Facet{{"title", "x"}, {"status", "draft"}}},
		{"a type that is not a string", "type: 7\n", []string{"type:type"}, []Facet{{"status", "draft"}}},
		{"another type's field", "type: adr\nnumber: '7'\nverdict: approved\n", nil,
			[]Facet{{"type", "adr"}, {"number", "7"}, {"status", "proposed"}}}, // not an adr's: no facet
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := s.ValidateFile(note(c.fm))
			if got := rules(r); !reflect.DeepEqual(got, c.rules) {
				t.Errorf("diagnostics = %+v, want %v", r.Diagnostics, c.rules)
			}
			if !reflect.DeepEqual(r.Facets, c.facets) {
				t.Errorf("facets = %v\nwant     %v", r.Facets, c.facets)
			}
		})
	}
	r := s.ValidateFile(note("type: memo\n"))
	if d := r.Diagnostics[0]; d.Line != 2 || d.Message != `unknown type "memo"` {
		t.Errorf("unknown type = %+v, want line 2, unknown type \"memo\"", d)
	}
}

// strict: true reports what the document's own type does not declare: a review's field on an adr,
// not on a review; a document of no known type is checked against common.
func TestValidateV2StrictPerType(t *testing.T) {
	s := mustParse(t, "strict: true\n"+kbSchemaV2)
	cases := []struct {
		name, fm string
		rules    []string
	}{
		{"a review's field on an adr", "type: adr\nnumber: '7'\nverdict: approved\n", []string{"verdict:unknown"}},
		{"on a review", "type: review\nreviewer: kim\nverdict: approved\nstatus: draft\n", nil},
		{"an adr's field on a note", "type: note\nnumber: '7'\n", []string{"number:unknown"}},
		{"on a memo", "type: memo\nverdict: approved\n", []string{"type:unknown_type", "verdict:unknown"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rules(s.ValidateFile(note(c.fm))); !reflect.DeepEqual(got, c.rules) {
				t.Errorf("diagnostics = %v, want %v", got, c.rules)
			}
		})
	}
	r := s.ValidateFile(note("type: adr\nnumber: '7'\nverdict: approved\n"))
	if d := r.Diagnostics[0]; d.Line != 4 || !strings.Contains(d.Message, `type "adr"`) {
		t.Errorf("strict = %+v, want line 4 naming type \"adr\"", d)
	}
	if loose := mustParse(t, kbSchemaV2); len(loose.ValidateFile(note("type: adr\nnumber: '7'\nverdict: x\n")).Diagnostics) != 0 {
		t.Error("a loose schema reported another type's field")
	}
}

// A type that makes a field required may narrow its enum past common's default: a required field
// is written, so common's default never answers for it.
func TestParseV2RequiredDropsCommonsDefault(t *testing.T) {
	s := mustParse(t, "version: 2\ndiscriminator: type\ncommon:\n  type: {type: string}\n  status: {type: string, default: draft}\n"+
		"types:\n  review:\n    status: {type: string, enum: [approved], required: true}\n")
	if got := rules(s.ValidateFile(note("type: review\n"))); !reflect.DeepEqual(got, []string{"status:required"}) {
		t.Errorf("a review without status = %v, want status:required", got)
	}
	r := s.ValidateFile(note("type: memo\n"))
	if !reflect.DeepEqual(r.Facets, []Facet{{"type", "memo"}, {"status", "draft"}}) {
		t.Errorf("a memo's facets = %v, want common's default", r.Facets)
	}
}

// A version 2 schema that is not a valid one is refused, with its line, as a version 1 schema is.
func TestParseV2ErrorsAreLineAware(t *testing.T) {
	const head = "version: 2\ndiscriminator: type\ncommon:\n  type: {type: string, enum: [adr, review]}\n  status: {type: string, enum: [a, b], required: true}\n  n: {type: integer, default: 1, enum: [1, 2]}\n"
	cases := []struct {
		name, src string
		line      int
		msg       string
	}{
		{"conflicting types across types", head + "types:\n  adr:\n    x: {type: string}\n  review:\n    x: {type: integer}\n", 11,
			`field "x": types.review declares it integer, but line 9 declares it string`},
		{"a type changes common's type", head + "types:\n  adr:\n    status: {type: integer}\n", 9,
			`field "status": types.adr declares it integer, but line 5 declares it string`},
		{"a type changes a list's item type", "version: 2\ndiscriminator: type\ncommon:\n  type: {type: string}\n  tags: {type: list}\ntypes:\n  adr:\n    tags: {type: list, item_type: integer}\n", 8,
			"declares it list of integer, but line 5 declares it list of string"},
		{"a type widens an enum", head + "types:\n  adr:\n    status: {type: string, enum: [a, c]}\n", 9, `enum value "c" is not one of common's enum`},
		{"a type makes a required field optional", head + "types:\n  adr:\n    status: {type: string, required: false}\n", 9, "cannot make a field common requires optional"},
		{"a type defaults a required field", head + "types:\n  adr:\n    status: {type: string, default: a}\n", 9, "a required field has no default"},
		{"a narrowed enum leaves out the default", head + "types:\n  adr:\n    n: {type: integer, enum: [2]}\n", 9, `leaves out its default "1"`},
		{"a type outside the discriminator's enum", head + "types:\n  memo:\n", 8, `type "memo" is not one of type's enum`},
		{"a type declared twice", head + "types:\n  adr:\n  adr:\n", 9, `type "adr" is declared twice`},
		{"a field declared twice in a type", head + "types:\n  adr:\n    x: {type: string}\n    x: {type: string}\n", 10, `field "x" is declared twice`},
		{"no discriminator", "version: 2\ncommon:\n  type: {type: string}\n", 1, "needs a discriminator"},
		{"no common", "version: 2\ndiscriminator: type\n", 1, "needs a common mapping"},
		{"discriminator not in common", "version: 2\ndiscriminator: kind\ncommon:\n  type: {type: string}\n", 2, `"kind" must be a field of common`},
		{"discriminator not a string", "version: 2\ndiscriminator: n\ncommon:\n  n: {type: integer}\n", 2, "must be a string field, not integer"},
		{"types not a mapping", head + "types: [adr]\n", 7, "types must be a mapping"},
		{"frontmatter in version 2", "version: 2\nfrontmatter: {}\n", 2, "under common and types, not frontmatter"},
		{"unknown top key", "version: 2\nfields: {}\n", 2, `unknown key "fields"`},
		{"a bad field in a type", head + "types:\n  adr:\n    x: {type: text}\n", 9, "type must be one of"},
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

// MaxFields bounds every declaration in a version 2 schema, common's and the types' together.
func TestParseV2CountsEveryDeclaration(t *testing.T) {
	var b strings.Builder
	b.WriteString("version: 2\ndiscriminator: type\ncommon:\n  type: {type: string}\ntypes:\n")
	for i := range MaxFields {
		fmt.Fprintf(&b, "  t%d:\n    f%d: {type: string}\n", i, i)
	}
	_, err := Parse([]byte(b.String()))
	var se *Error
	if !errors.As(err, &se) || !strings.Contains(se.Msg, fmt.Sprintf("more than %d fields", MaxFields)) {
		t.Fatalf("err = %v, want more than %d fields", err, MaxFields)
	}
}
