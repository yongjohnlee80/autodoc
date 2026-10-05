package schema

import (
	"fmt"
	"strconv"

	"github.com/yongjohnlee80/golib/parse/markdown"
	pyaml "github.com/yongjohnlee80/golib/parse/yaml"
	"github.com/yongjohnlee80/golib/yaml"
)

// The rules a Diagnostic names.
const (
	RuleYAML     = "yaml"     // the frontmatter is not well-formed YAML: nothing in it is checked
	RuleMapping  = "mapping"  // the frontmatter is YAML, but not a mapping of fields
	RuleRequired = "required" // a required field is missing or empty
	RuleType     = "type"     // a field's value is not of its declared type
	RuleEnum     = "enum"     // a field's value is not one its enum allows
	RuleUnknown  = "unknown"  // a strict schema does not declare the field
	// RuleUnknownType: a version 2 schema names no type the document's discriminator gives, so the
	// document is checked against common alone
	RuleUnknownType = "unknown_type"
)

// Diagnostic is one problem in a file's frontmatter: the field (when it is one field's), the file's
// line it is on (1-based), the rule it breaks, and a message for a person.
type Diagnostic struct {
	Field   string
	Line    int
	Rule    string
	Message string
}

// Facet is one typed value of a declared field: what an exact filter field:value matches. A list
// field has one facet per item.
type Facet struct{ Field, Value string }

// Result is what a file's frontmatter declares under a schema: its valid fields' facets, defaults
// included, and its diagnostics, in source order.
type Result struct {
	Facets      []Facet
	Diagnostics []Diagnostic
}

// ValidateFile checks a Markdown file's frontmatter, parsing the file as the indexer does. A nil
// schema still reports frontmatter that is not well-formed, which is worth saying before a save.
func (s *Schema) ValidateFile(src []byte) Result {
	doc := markdown.Parse(src, markdown.GFM(), markdown.Obsidian())
	if fm := doc.Root.FirstChild; fm != nil && fm.Kind == markdown.KindFrontmatter {
		return s.Validate(fm.Literal, true)
	}
	return s.Validate(nil, false)
}

// Validate checks a file's frontmatter: fm is the text between its fences, which begins on the
// file's line 2. present says the file has frontmatter at all; a file without any still has its
// required fields reported, against line 1.
func (s *Schema) Validate(fm []byte, present bool) Result {
	const firstLine = 2 // the line after the opening "---"
	var r Result
	var root *pyaml.Node
	var doc *pyaml.Document
	var tags yaml.Tags
	if present {
		st, err := pyaml.Parse(fm)
		if err == nil && len(st.Docs) > 1 {
			r.Diagnostics = append(r.Diagnostics, Diagnostic{Line: firstLine, Rule: RuleYAML,
				Message: fmt.Sprintf("the frontmatter holds %d YAML documents, not one", len(st.Docs))})
			return r
		}
		if err == nil && len(st.Docs) == 1 {
			doc = st.Docs[0]
			if tags, err = yaml.Resolve(doc, yaml.Core); err == nil {
				// Evaluate refuses what Resolve admits: duplicate keys, an alias bomb
				_, err = yaml.Evaluate(doc, yaml.Core)
			}
			root = doc.Root
		}
		if err != nil {
			line := firstLine
			if se, ok := yamlError(err).(*Error); ok && se.Line > 0 {
				line = se.Line + firstLine - 1
				err = fmt.Errorf("%s", se.Msg)
			}
			r.Diagnostics = append(r.Diagnostics, Diagnostic{Line: line, Rule: RuleYAML, Message: err.Error()})
			return r
		}
		if root != nil && root.Kind != pyaml.KindMapping && nodeValue(root, tags) != nil {
			r.Diagnostics = append(r.Diagnostics, Diagnostic{Line: doc.Position(root.Span.Start).Line + firstLine - 1,
				Rule: RuleMapping, Message: "the frontmatter is not a mapping of fields"})
			return r
		}
	}
	if s == nil {
		return r
	}
	lineOf := func(n *pyaml.Node) int { return doc.Position(n.Span.Start).Line + firstLine - 1 }
	set, typ, known := s.fieldsFor(root, tags)
	written := map[string]bool{}
	if root != nil && root.Kind == pyaml.KindMapping {
		for _, pr := range root.Pairs {
			name, ok := nodeValue(pr.Key, tags).(string)
			if !ok {
				if s.Strict {
					r.Diagnostics = append(r.Diagnostics, Diagnostic{Line: lineOf(pr.Key), Rule: RuleUnknown,
						Message: "a field name must be a string"})
				}
				continue
			}
			f, declared := set.field(name)
			if !declared {
				if s.Strict {
					msg := fmt.Sprintf("%s is not a field of this workspace's schema", strconv.Quote(name))
					if known {
						msg = fmt.Sprintf("%s is not a field of type %s in this workspace's schema", strconv.Quote(name), strconv.Quote(typ))
					}
					r.Diagnostics = append(r.Diagnostics, Diagnostic{Field: name, Line: lineOf(pr.Key), Rule: RuleUnknown, Message: msg})
				}
				continue
			}
			v := nodeValue(pr.Value, tags)
			if v == nil {
				continue // "field:" with no value is a missing field: required, or its default
			}
			written[name] = true
			vals, why := typed(f, v)
			if why != "" {
				r.Diagnostics = append(r.Diagnostics, Diagnostic{Field: name, Line: lineOf(pr.Value), Rule: RuleType,
					Message: fmt.Sprintf("%s %s", name, why)})
				continue
			}
			if bad := outsideEnum(f, vals); bad != "" {
				r.Diagnostics = append(r.Diagnostics, Diagnostic{Field: name, Line: lineOf(pr.Value), Rule: RuleEnum,
					Message: fmt.Sprintf("%s: %s is not one of %v", name, strconv.Quote(bad), f.Enum)})
				continue
			}
			for _, val := range vals {
				r.Facets = append(r.Facets, Facet{Field: name, Value: val})
			}
			if typ != "" && !known && name == s.Discriminator {
				r.Diagnostics = append(r.Diagnostics, Diagnostic{Field: name, Line: lineOf(pr.Value), Rule: RuleUnknownType,
					Message: fmt.Sprintf("unknown type %s", strconv.Quote(typ))})
			}
		}
	}
	at := 1 // a missing field is the frontmatter's, or the file's first line when it has none
	for _, f := range set.fields {
		if written[f.Name] {
			continue
		}
		switch {
		case f.Required:
			r.Diagnostics = append(r.Diagnostics, Diagnostic{Field: f.Name, Line: at, Rule: RuleRequired,
				Message: fmt.Sprintf("%s is required", f.Name)})
		case f.Default != nil:
			for _, val := range f.Default {
				r.Facets = append(r.Facets, Facet{Field: f.Name, Value: val})
			}
		}
	}
	return r
}

// fieldsFor is the fields a document is checked against: a version 1 schema's, or a version 2
// schema's common merged with the document's type. typ is that type when the discriminator's value
// is one its declaration admits (else the discriminator's own diagnostic says why), and known says
// the schema names it; a type it does not name is checked against common alone.
func (s *Schema) fieldsFor(root *pyaml.Node, tags yaml.Tags) (set fieldSet, typ string, known bool) {
	if s.Discriminator == "" || root == nil || root.Kind != pyaml.KindMapping {
		return s.common, "", false
	}
	f, _ := s.common.field(s.Discriminator)
	for _, pr := range root.Pairs {
		if name, ok := nodeValue(pr.Key, tags).(string); !ok || name != s.Discriminator {
			continue
		}
		v := nodeValue(pr.Value, tags)
		if v == nil {
			break
		}
		vals, why := typed(f, v)
		if why != "" || outsideEnum(f, vals) != "" {
			break
		}
		if t, ok := s.types[vals[0]]; ok {
			return t, vals[0], true
		}
		return s.common, vals[0], false
	}
	return s.common, "", false
}

// FacetValue reads text typed as a filter on field (status:active, count:7) into the facet value it
// matches, or says why the field's type cannot hold it. The text is read as YAML's Core schema reads
// a plain scalar, as the file's own value was.
func (s *Schema) FacetValue(field, text string) (string, error) {
	f, ok := s.Field(field)
	if !ok {
		return "", fmt.Errorf("%s is not a field of this workspace's schema", strconv.Quote(field))
	}
	t := f.Type
	if t == List {
		t = f.ItemType
	}
	if v, ok := facetValue(t, plainScalar(text)); ok {
		return v, nil
	}
	if t == String {
		return text, nil // "007" or "true" typed for a string field means that text
	}
	return "", fmt.Errorf("%s takes %s", field, article(t))
}

// plainScalar is text as the Core schema resolves it when written unquoted.
func plainScalar(text string) any {
	st, err := pyaml.Parse([]byte(text))
	if err != nil || len(st.Docs) != 1 || st.Docs[0].Root == nil || st.Docs[0].Root.Kind != pyaml.KindScalar {
		return text
	}
	tags, err := yaml.Resolve(st.Docs[0], yaml.Core)
	if err != nil {
		return text
	}
	return nodeValue(st.Docs[0].Root, tags)
}
