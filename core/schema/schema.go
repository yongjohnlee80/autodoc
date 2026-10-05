// Package schema is a workspace's frontmatter schema (ADR 0212 §5): the fields its Markdown files
// declare in frontmatter, their types, and which are required. One validator serves the daemon,
// which indexes typed facets and diagnostics, and the editor, which checks unsaved text, so the two
// never disagree. Validation never rewrites a file: a default is a value the facet index answers
// for a missing field, not an edit.
//
// A schema file, version 1:
//
//	version: 1
//	strict: false        # optional: report fields the schema does not name
//	frontmatter:
//	  type:   {type: string, enum: [note, adr], required: true}
//	  status: {type: string, enum: [draft, active], default: active}
//	  tags:   {type: list, item_type: string}
//	  created: {type: date}
//
// The types are string, integer, number, boolean, date (YYYY-MM-DD, or an RFC 3339 time, whose
// date is its facet) and list. A list's items are strings unless item_type names another scalar
// type; a single scalar where a list is declared is a list of one, as Obsidian writes tags.
//
// A schema file, version 2, declares fields by document type (ADR 1791209945 §4.3), and still
// validates every document:
//
//	version: 2
//	strict: false
//	discriminator: type  # the common string field whose value picks a document's type
//	common:              # every document
//	  type:   {type: string, required: true}
//	  status: {type: string, required: true}
//	types:               # merged over common when type has this value
//	  adr:
//	    status: {type: string, enum: [proposed, accepted]}
//	  review:
//	    verdict: {type: string, enum: [approved, rejected], required: true}
//
// A type may narrow a common field (a smaller enum, required, its own default) but never change
// its type, and one name has one type across the file. A document whose type the schema does not
// name is checked against common alone, with a diagnostic.
package schema

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	pyaml "github.com/yongjohnlee80/golib/parse/yaml"
	"github.com/yongjohnlee80/golib/yaml"
)

// Version is the newest schema version this build reads, and MinVersion the oldest.
const (
	Version    = 2
	MinVersion = 1
)

// MaxSize is the largest schema file accepted, and MaxFields the most fields it may declare (in a
// version 2 schema, every declaration in common and the types).
const (
	MaxSize   = 256 << 10
	MaxFields = 256
	maxEnum   = 1024
)

// Type is a field's type.
type Type string

const (
	String  Type = "string"
	Integer Type = "integer"
	Number  Type = "number"
	Boolean Type = "boolean"
	Date    Type = "date"
	List    Type = "list"
)

func (t Type) scalar() bool {
	switch t {
	case String, Integer, Number, Boolean, Date:
		return true
	}
	return false
}

// Field is one declared frontmatter field.
type Field struct {
	Name     string
	Type     Type
	ItemType Type // List only; String when the schema does not say
	Required bool
	Enum     []string // facet values; empty: any value of the type
	Default  []string // facet values a missing field answers with; nil when none
	Line     int      // where the schema declares it
}

// Schema is a parsed, valid schema.
type Schema struct {
	Version int // 1 or 2
	Strict  bool
	// Fields is every declared field, in the schema's order: in version 2 the union of common and
	// the types, each as common declares it or else as the first type that does. It is what the
	// query filters by; a document is checked against its own type's fields.
	Fields []Field
	byName map[string]int
	// Discriminator is the common field whose value picks a document's type; "" in version 1
	Discriminator string
	common        fieldSet            // version 1: every field
	types         map[string]fieldSet // by discriminator value: common merged with the type's own
}

// fieldSet is the fields one document is checked against, in the schema's order.
type fieldSet struct {
	fields []Field
	byName map[string]int
}

func (fs fieldSet) field(name string) (Field, bool) {
	i, ok := fs.byName[name]
	if !ok {
		return Field{}, false
	}
	return fs.fields[i], true
}

func (fs *fieldSet) add(f Field) {
	fs.byName[f.Name] = len(fs.fields)
	fs.fields = append(fs.fields, f)
}

// Field is the declaration of name.
// Declared reports whether the schema declares name; a nil schema declares nothing. With
// FacetValue it makes a schema the query's facet fields (golib's search/query.Fields).
func (s *Schema) Declared(name string) bool {
	_, ok := s.Field(name)
	return ok
}

func (s *Schema) Field(name string) (Field, bool) {
	if s == nil {
		return Field{}, false
	}
	i, ok := s.byName[name]
	if !ok {
		return Field{}, false
	}
	return s.Fields[i], true
}

// Error is a schema file that is not a valid schema: the line it is on (1-based; 0 when it is not
// one line's) and why.
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("schema: line %d: %s", e.Line, e.Msg)
	}
	return "schema: " + e.Msg
}

// ErrTooLarge is a schema file over MaxSize.
var ErrTooLarge = &Error{Msg: fmt.Sprintf("the schema file is over %d KiB", MaxSize>>10)}

// Parse reads a schema file.
func Parse(src []byte) (*Schema, error) {
	if len(src) > MaxSize {
		return nil, ErrTooLarge
	}
	st, err := pyaml.Parse(src)
	if err != nil {
		return nil, yamlError(err)
	}
	if len(st.Docs) != 1 || st.Docs[0].Root == nil {
		return nil, &Error{Msg: "a schema file holds exactly one YAML document"}
	}
	doc := st.Docs[0]
	tags, err := yaml.Resolve(doc, yaml.Core)
	if err != nil {
		return nil, yamlError(err)
	}
	p := parser{doc: doc, tags: tags}
	return p.schema(doc.Root)
}

type parser struct {
	doc  *pyaml.Document
	tags yaml.Tags
}

func (p parser) line(n *pyaml.Node) int { return p.doc.Position(n.Span.Start).Line }

func (p parser) fail(n *pyaml.Node, format string, args ...any) error {
	return &Error{Line: p.line(n), Msg: fmt.Sprintf(format, args...)}
}

func (p parser) schema(root *pyaml.Node) (*Schema, error) {
	if root.Kind != pyaml.KindMapping {
		return nil, p.fail(root, "the schema must be a mapping with version and frontmatter")
	}
	if p.version(root) == 2 {
		return p.schemaV2(root)
	}
	s := &Schema{Version: 1, byName: map[string]int{}}
	var version, fields *pyaml.Node
	for _, pr := range root.Pairs {
		key, err := p.key(pr.Key)
		if err != nil {
			return nil, err
		}
		switch key {
		case "version":
			version = pr.Value
		case "strict":
			b, err := p.boolean(pr.Value, "strict")
			if err != nil {
				return nil, err
			}
			s.Strict = b
		case "frontmatter":
			fields = pr.Value
		default:
			return nil, p.fail(pr.Key, "unknown key %q (the keys are version, strict and frontmatter)", key)
		}
	}
	if version == nil {
		return nil, p.fail(root, "the schema needs version: %d or %d", MinVersion, Version)
	}
	if v, ok := p.value(version).(int64); !ok || v != 1 {
		return nil, p.fail(version, "version must be %d or %d", MinVersion, Version)
	}
	if fields == nil {
		return nil, p.fail(root, "the schema needs a frontmatter mapping of fields")
	}
	declared := 0
	set, _, err := p.fields(fields, "frontmatter", &declared)
	if err != nil {
		return nil, err
	}
	s.Fields, s.byName, s.common = set.fields, set.byName, set
	return s, nil
}

// version is the schema's version as written, or 0 when it does not write one as an integer; the
// keys are checked by the version's own reader.
func (p parser) version(root *pyaml.Node) int64 {
	for _, pr := range root.Pairs {
		if key, ok := p.value(pr.Key).(string); ok && key == "version" {
			v, _ := p.value(pr.Value).(int64)
			return v
		}
	}
	return 0
}

// schemaV2 reads a version 2 schema: common, and each type's fields merged over it.
func (p parser) schemaV2(root *pyaml.Node) (*Schema, error) {
	s := &Schema{Version: 2, byName: map[string]int{}}
	var disc, common, types *pyaml.Node
	for _, pr := range root.Pairs {
		key, err := p.key(pr.Key)
		if err != nil {
			return nil, err
		}
		switch key {
		case "version": // read by version
		case "strict":
			b, err := p.boolean(pr.Value, "strict")
			if err != nil {
				return nil, err
			}
			s.Strict = b
		case "discriminator":
			disc = pr.Value
		case "common":
			common = pr.Value
		case "types":
			types = pr.Value
		case "frontmatter":
			return nil, p.fail(pr.Key, "a version 2 schema declares its fields under common and types, not frontmatter")
		default:
			return nil, p.fail(pr.Key, "unknown key %q (the keys are version, strict, discriminator, common and types)", key)
		}
	}
	if common == nil {
		return nil, p.fail(root, "a version 2 schema needs a common mapping of fields")
	}
	if disc == nil {
		return nil, p.fail(root, "a version 2 schema needs a discriminator: the common field whose value picks a document's type")
	}
	name, ok := p.value(disc).(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, p.fail(disc, "discriminator must be a field name")
	}
	s.Discriminator = name
	declared := 0
	set, _, err := p.fields(common, "common", &declared)
	if err != nil {
		return nil, err
	}
	s.common = set
	df, ok := set.field(name)
	if !ok {
		return nil, p.fail(disc, "the discriminator %q must be a field of common", name)
	}
	if df.Type != String {
		return nil, p.fail(disc, "the discriminator %q must be a string field, not %s", name, df.kind())
	}
	for _, f := range set.fields {
		s.byName[f.Name] = len(s.Fields)
		s.Fields = append(s.Fields, f)
	}
	s.types = map[string]fieldSet{}
	if p.value(types) == nil {
		return s, nil // no types, or "types:" with nothing under it: every document is common's
	}
	if types.Kind != pyaml.KindMapping {
		return nil, p.fail(types, "types must be a mapping of %s values to their fields", name)
	}
	for _, pr := range types.Pairs {
		typ, err := p.key(pr.Key)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(typ) == "" {
			return nil, p.fail(pr.Key, "a type needs a name")
		}
		if _, dup := s.types[typ]; dup {
			return nil, p.fail(pr.Key, "type %q is declared twice", typ)
		}
		if outsideEnum(df, []string{typ}) != "" {
			return nil, p.fail(pr.Key, "type %q is not one of %s's enum, so no document can have it", typ, name)
		}
		where := "types." + typ
		own, optional, err := p.fields(pr.Value, where, &declared)
		if err != nil {
			return nil, err
		}
		for _, f := range own.fields {
			u, ok := s.Field(f.Name)
			if !ok {
				s.byName[f.Name] = len(s.Fields)
				s.Fields = append(s.Fields, f)
				continue
			}
			if u.Type != f.Type || u.ItemType != f.ItemType {
				return nil, &Error{Line: f.Line, Msg: fmt.Sprintf("field %q: %s declares it %s, but line %d declares it %s (a field has one type in every document type)",
					f.Name, where, f.kind(), u.Line, u.kind())}
			}
		}
		merged, err := merge(set, own, optional, where)
		if err != nil {
			return nil, err
		}
		s.types[typ] = merged
	}
	return s, nil
}

// merge is common with a type's own fields over it. A type narrows a common field and never widens
// it: its enum is within common's, and it cannot make a required field optional or give it a
// default. The fields' types already agree.
func merge(common, own fieldSet, optional map[string]bool, where string) (fieldSet, error) {
	if len(own.fields) == 0 {
		return common, nil // shared: a type with no fields of its own is common's
	}
	m := fieldSet{byName: map[string]int{}}
	for _, c := range common.fields {
		o, ok := own.field(c.Name)
		if !ok {
			m.add(c)
			continue
		}
		f := c
		f.Line = o.Line
		if o.Enum != nil {
			if bad := outsideEnum(c, o.Enum); bad != "" {
				return m, &Error{Line: o.Line, Msg: fmt.Sprintf("field %q: %s's enum value %q is not one of common's enum (a type narrows a field, never widens it)", c.Name, where, bad)}
			}
			f.Enum = o.Enum
		}
		if c.Required && optional[c.Name] {
			return m, &Error{Line: o.Line, Msg: fmt.Sprintf("field %q: %s cannot make a field common requires optional", c.Name, where)}
		}
		f.Required = c.Required || o.Required
		if o.Default != nil {
			if f.Required {
				return m, &Error{Line: o.Line, Msg: fmt.Sprintf("field %q: a required field has no default (it must be written)", c.Name)}
			}
			f.Default = o.Default
		}
		if f.Required {
			f.Default = nil // a required field is written, so a default from common never answers for it
		}
		if bad := outsideEnum(f, f.Default); bad != "" {
			return m, &Error{Line: o.Line, Msg: fmt.Sprintf("field %q: %s's enum leaves out its default %q", c.Name, where, bad)}
		}
		m.add(f)
	}
	for _, o := range own.fields {
		if _, ok := common.field(o.Name); !ok {
			m.add(o)
		}
	}
	return m, nil
}

// fields reads a mapping of field names to their declarations, where says which ("frontmatter",
// "common", "types.adr"). declared counts the declarations read so far, against MaxFields. It also
// answers which fields write required: false, which a type may not say of a field common requires.
func (p parser) fields(n *pyaml.Node, where string, declared *int) (fieldSet, map[string]bool, error) {
	set := fieldSet{byName: map[string]int{}}
	optional := map[string]bool{}
	if p.value(n) == nil {
		return set, optional, nil // "frontmatter:" with nothing under it declares no fields
	}
	if n.Kind != pyaml.KindMapping {
		return set, nil, p.fail(n, "%s must be a mapping of field names to their declarations", where)
	}
	if *declared += len(n.Pairs); *declared > MaxFields {
		return set, nil, p.fail(n, "more than %d fields", MaxFields)
	}
	for _, pr := range n.Pairs {
		name, err := p.key(pr.Key)
		if err != nil {
			return set, nil, err
		}
		if strings.TrimSpace(name) == "" {
			return set, nil, p.fail(pr.Key, "a field needs a name")
		}
		if _, dup := set.byName[name]; dup {
			return set, nil, p.fail(pr.Key, "field %q is declared twice", name)
		}
		f, wroteRequired, err := p.field(name, pr.Key, pr.Value)
		if err != nil {
			return set, nil, err
		}
		if wroteRequired && !f.Required {
			optional[name] = true
		}
		set.add(f)
	}
	return set, optional, nil
}

// field reads one declaration, and whether it writes required at all.
func (p parser) field(name string, at, decl *pyaml.Node) (Field, bool, error) {
	f, wroteRequired := Field{Name: name, Line: p.line(at), ItemType: String}, false
	if decl.Kind != pyaml.KindMapping {
		return f, false, p.fail(decl, "field %q: the declaration must be a mapping with a type", name)
	}
	var enum, def *pyaml.Node
	for _, pr := range decl.Pairs {
		key, err := p.key(pr.Key)
		if err != nil {
			return f, false, err
		}
		switch key {
		case "type", "item_type":
			s, ok := p.value(pr.Value).(string)
			t := Type(s)
			if !ok || (!t.scalar() && t != List) || (key == "item_type" && t == List) {
				return f, false, p.fail(pr.Value, "field %q: %s must be one of %s", name, key, typeNames(key == "type"))
			}
			if key == "type" {
				f.Type = t
			} else {
				f.ItemType = t
			}
		case "required":
			b, err := p.boolean(pr.Value, "field "+strconv.Quote(name)+": required")
			if err != nil {
				return f, false, err
			}
			f.Required, wroteRequired = b, true
		case "enum":
			enum = pr.Value
		case "default":
			def = pr.Value
		default:
			return f, false, p.fail(pr.Key, "field %q: unknown key %q (the keys are type, item_type, required, enum and default)", name, key)
		}
	}
	if f.Type == "" {
		return f, false, p.fail(decl, "field %q needs a type", name)
	}
	itemType := f.Type
	if f.Type == List {
		itemType = f.ItemType
	} else {
		f.ItemType = ""
	}
	if enum != nil {
		if enum.Kind != pyaml.KindSequence || len(enum.Items) == 0 {
			return f, false, p.fail(enum, "field %q: enum must be a non-empty list", name)
		}
		if len(enum.Items) > maxEnum {
			return f, false, p.fail(enum, "field %q: enum has more than %d values", name, maxEnum)
		}
		seen := map[string]bool{}
		for _, it := range enum.Items {
			v, ok := facetValue(itemType, p.value(it))
			if !ok {
				return f, false, p.fail(it, "field %q: enum value is not a %s", name, itemType)
			}
			if !seen[v] {
				seen[v] = true
				f.Enum = append(f.Enum, v)
			}
		}
	}
	if def != nil {
		if f.Required {
			return f, false, p.fail(def, "field %q: a required field has no default (it must be written)", name)
		}
		vals, diag := typed(f, p.value(def))
		if diag != "" {
			return f, false, p.fail(def, "field %q: default %s", name, diag)
		}
		if bad := outsideEnum(f, vals); bad != "" {
			return f, false, p.fail(def, "field %q: default %q is not one of its enum", name, bad)
		}
		f.Default = vals
		if f.Default == nil {
			f.Default = []string{} // an explicit empty list
		}
	}
	return f, wroteRequired, nil
}

// kind is the field's type as a schema error names it: "string", or "list of integer".
func (f Field) kind() string {
	if f.Type == List {
		return "list of " + string(f.ItemType)
	}
	return string(f.Type)
}

func typeNames(withList bool) string {
	if withList {
		return "string, integer, number, boolean, date or list"
	}
	return "string, integer, number, boolean or date"
}

func (p parser) key(n *pyaml.Node) (string, error) {
	s, ok := p.value(n).(string)
	if !ok {
		return "", p.fail(n, "a key must be a string")
	}
	return s, nil
}

func (p parser) boolean(n *pyaml.Node, what string) (bool, error) {
	b, ok := p.value(n).(bool)
	if !ok {
		return false, p.fail(n, "%s must be true or false", what)
	}
	return b, nil
}

// value is a node as the Core schema reads it: a scalar's native value, a sequence's items, nil
// for null, or the node itself for a mapping (the callers walk mappings).
func (p parser) value(n *pyaml.Node) any { return nodeValue(n, p.tags) }

func nodeValue(n *pyaml.Node, tags yaml.Tags) any {
	if n == nil {
		return nil
	}
	if n.Kind == pyaml.KindAlias {
		if n.Target == nil {
			return nil
		}
		return nodeValue(n.Target, tags)
	}
	switch n.Kind {
	case pyaml.KindSequence:
		out := make([]any, len(n.Items))
		for i, it := range n.Items {
			out[i] = nodeValue(it, tags)
		}
		return out
	case pyaml.KindMapping:
		return n
	}
	s := string(n.Value)
	switch tags[n] {
	case yaml.TagNull:
		return nil
	case yaml.TagBool:
		return strings.EqualFold(s, "true")
	case yaml.TagInt:
		if v, err := coreInt(s); err == nil {
			return v
		}
	case yaml.TagFloat:
		if v, ok := coreFloat(s); ok {
			return v
		}
	}
	return s
}

func coreInt(s string) (int64, error) {
	switch {
	case strings.HasPrefix(s, "0o"):
		return strconv.ParseInt(s[2:], 8, 64)
	case strings.HasPrefix(s, "0x"):
		return strconv.ParseInt(s[2:], 16, 64)
	}
	return strconv.ParseInt(s, 10, 64)
}

func coreFloat(s string) (float64, bool) {
	switch strings.ToLower(strings.TrimLeft(s, "+")) {
	case ".inf":
		return math.Inf(1), true
	case "-.inf":
		return math.Inf(-1), true
	case ".nan":
		return math.NaN(), true
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// typed reads a field's value as its facet values, or says why it is not of the field's type.
func typed(f Field, v any) ([]string, string) {
	if f.Type != List {
		s, ok := facetValue(f.Type, v)
		if !ok {
			return nil, "is " + kindOf(v) + ", not " + article(f.Type)
		}
		return []string{s}, ""
	}
	items, isList := v.([]any)
	if !isList {
		items = []any{v} // a lone scalar is a list of one
	}
	var out []string
	for i, it := range items {
		s, ok := facetValue(f.ItemType, it)
		if !ok {
			if isList {
				return nil, fmt.Sprintf("item %d is %s, not %s", i+1, kindOf(it), article(f.ItemType))
			}
			return nil, "is " + kindOf(it) + ", not a list of " + string(f.ItemType) + "s"
		}
		out = append(out, s)
	}
	return out, ""
}

// facetValue is v as a facet of type t, the one string an exact filter compares.
func facetValue(t Type, v any) (string, bool) {
	switch t {
	case String:
		s, ok := v.(string)
		return s, ok
	case Integer:
		n, ok := v.(int64)
		return strconv.FormatInt(n, 10), ok
	case Number:
		switch n := v.(type) {
		case int64:
			return strconv.FormatInt(n, 10), true
		case float64:
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return "", false
			}
			if n == math.Trunc(n) && math.Abs(n) < 1<<53 {
				return strconv.FormatInt(int64(n), 10), true
			}
			return strconv.FormatFloat(n, 'g', -1, 64), true
		}
	case Boolean:
		b, ok := v.(bool)
		return strconv.FormatBool(b), ok
	case Date:
		s, ok := v.(string)
		if !ok {
			return "", false
		}
		return dateOf(s)
	}
	return "", false
}

func dateOf(s string) (string, bool) {
	if d, err := time.Parse(time.DateOnly, s); err == nil {
		return d.Format(time.DateOnly), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format(time.DateOnly), true
	}
	return "", false
}

func kindOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "empty"
	case string:
		return "a string"
	case int64:
		return "an integer"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case []any:
		return "a list"
	case *pyaml.Node:
		_ = x
		return "a mapping"
	}
	return "a value"
}

func article(t Type) string {
	switch t {
	case Integer:
		return "an integer"
	case Date:
		return "a date (YYYY-MM-DD)"
	case List:
		return "a list"
	}
	return "a " + string(t)
}

func outsideEnum(f Field, vals []string) string {
	if len(f.Enum) == 0 {
		return ""
	}
	for _, v := range vals {
		found := false
		for _, e := range f.Enum {
			if v == e {
				found = true
				break
			}
		}
		if !found {
			return v
		}
	}
	return ""
}

// yamlError is a YAML parse or resolve error as a line-aware schema error.
func yamlError(err error) error {
	var pe *pyaml.Error
	if errors.As(err, &pe) {
		return &Error{Line: pe.Pos.Line, Msg: pe.Msg}
	}
	var ye *yaml.Error
	if errors.As(err, &ye) {
		return &Error{Line: ye.Pos.Line, Msg: ye.Msg}
	}
	return &Error{Msg: err.Error()}
}
