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

// Version is the one schema version this build reads.
const Version = 1

// MaxSize is the largest schema file accepted, and MaxFields the most fields it may declare.
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
	Strict bool
	Fields []Field // in the schema's order
	byName map[string]int
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
	s := &Schema{byName: map[string]int{}}
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
		return nil, p.fail(root, "the schema needs version: %d", Version)
	}
	if v, ok := p.value(version).(int64); !ok || v != Version {
		return nil, p.fail(version, "version must be %d", Version)
	}
	if fields == nil {
		return nil, p.fail(root, "the schema needs a frontmatter mapping of fields")
	}
	if p.value(fields) == nil {
		return s, nil // "frontmatter:" with nothing under it declares no fields
	}
	if fields.Kind != pyaml.KindMapping {
		return nil, p.fail(fields, "frontmatter must be a mapping of field names to their declarations")
	}
	if len(fields.Pairs) > MaxFields {
		return nil, p.fail(fields, "more than %d fields", MaxFields)
	}
	for _, pr := range fields.Pairs {
		name, err := p.key(pr.Key)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) == "" {
			return nil, p.fail(pr.Key, "a field needs a name")
		}
		if _, dup := s.byName[name]; dup {
			return nil, p.fail(pr.Key, "field %q is declared twice", name)
		}
		f, err := p.field(name, pr.Key, pr.Value)
		if err != nil {
			return nil, err
		}
		s.byName[name] = len(s.Fields)
		s.Fields = append(s.Fields, f)
	}
	return s, nil
}

func (p parser) field(name string, at, decl *pyaml.Node) (Field, error) {
	f := Field{Name: name, Line: p.line(at), ItemType: String}
	if decl.Kind != pyaml.KindMapping {
		return f, p.fail(decl, "field %q: the declaration must be a mapping with a type", name)
	}
	var enum, def *pyaml.Node
	for _, pr := range decl.Pairs {
		key, err := p.key(pr.Key)
		if err != nil {
			return f, err
		}
		switch key {
		case "type", "item_type":
			s, ok := p.value(pr.Value).(string)
			t := Type(s)
			if !ok || (!t.scalar() && t != List) || (key == "item_type" && t == List) {
				return f, p.fail(pr.Value, "field %q: %s must be one of %s", name, key, typeNames(key == "type"))
			}
			if key == "type" {
				f.Type = t
			} else {
				f.ItemType = t
			}
		case "required":
			b, err := p.boolean(pr.Value, "field "+strconv.Quote(name)+": required")
			if err != nil {
				return f, err
			}
			f.Required = b
		case "enum":
			enum = pr.Value
		case "default":
			def = pr.Value
		default:
			return f, p.fail(pr.Key, "field %q: unknown key %q (the keys are type, item_type, required, enum and default)", name, key)
		}
	}
	if f.Type == "" {
		return f, p.fail(decl, "field %q needs a type", name)
	}
	itemType := f.Type
	if f.Type == List {
		itemType = f.ItemType
	} else {
		f.ItemType = ""
	}
	if enum != nil {
		if enum.Kind != pyaml.KindSequence || len(enum.Items) == 0 {
			return f, p.fail(enum, "field %q: enum must be a non-empty list", name)
		}
		if len(enum.Items) > maxEnum {
			return f, p.fail(enum, "field %q: enum has more than %d values", name, maxEnum)
		}
		seen := map[string]bool{}
		for _, it := range enum.Items {
			v, ok := facetValue(itemType, p.value(it))
			if !ok {
				return f, p.fail(it, "field %q: enum value is not a %s", name, itemType)
			}
			if !seen[v] {
				seen[v] = true
				f.Enum = append(f.Enum, v)
			}
		}
	}
	if def != nil {
		if f.Required {
			return f, p.fail(def, "field %q: a required field has no default (it must be written)", name)
		}
		vals, diag := typed(f, p.value(def))
		if diag != "" {
			return f, p.fail(def, "field %q: default %s", name, diag)
		}
		if bad := outsideEnum(f, vals); bad != "" {
			return f, p.fail(def, "field %q: default %q is not one of its enum", name, bad)
		}
		f.Default = vals
		if f.Default == nil {
			f.Default = []string{} // an explicit empty list
		}
	}
	return f, nil
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
