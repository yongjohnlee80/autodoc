package schema

import (
	"testing"

	"github.com/yongjohnlee80/golib/search/query"
)

// TestASchemaIsTheQuerysFields: a schema declares its fields to the query, and reads their values;
// a nil schema, as a typed nil in the interface, declares nothing.
func TestASchemaIsTheQuerysFields(t *testing.T) {
	s, err := Parse([]byte("version: 1\nfrontmatter:\n  status:\n    type: string\n    enum: [draft, active]\n  count:\n    type: integer\n"))
	if err != nil {
		t.Fatal(err)
	}
	var f query.Fields = s
	if !f.Declared("status") || f.Declared("nope") {
		t.Error("Declared")
	}
	if v, err := f.FacetValue("count", "007"); err != nil || v != "7" {
		t.Errorf("FacetValue(count, 007) = %q, %v", v, err)
	}
	var none *Schema
	f = none
	if f.Declared("status") {
		t.Error("a nil schema declared a field")
	}
	if _, err := f.FacetValue("status", "draft"); err == nil {
		t.Error("a nil schema read a value")
	}
	rest, facets, err := query.Facets("status:draft words", nil, f)
	if err != nil || rest != "status:draft words" || facets != nil {
		t.Errorf("through a nil schema: %q %v %v", rest, facets, err)
	}
}
