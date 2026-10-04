package index

import (
	"strings"
	"testing"
)

func TestYAMLIndexesValuesWithKeyPathAndSourceSpan(t *testing.T) {
	env := newEnv(t, Options{Match: func(path string) bool { return strings.HasSuffix(path, ".yaml") }})
	source := "title: Deployment\ndatabase:\n  engine: PostgreSQL\n  port: 5432\n"
	env.put("settings.yaml", source)
	hits := env.search("postgresql", QueryOpts{}).Hits
	// the unit is the top-level entry: its key in the breadcrumb, the value's key path in the text
	if len(hits) != 1 || hits[0].Breadcrumb != "Deployment > database" || !strings.Contains(hits[0].Snippet, "engine") {
		t.Fatalf("YAML search hits = %+v", hits)
	}
	span := source[hits[0].ByteStart:hits[0].ByteEnd]
	if !strings.Contains(span, "PostgreSQL") {
		t.Fatalf("YAML source span %q misses scalar value", span)
	}
}

func TestInvalidYAMLStaysLexicallySearchable(t *testing.T) {
	env := newEnv(t, Options{Match: func(path string) bool { return strings.HasSuffix(path, ".yml") }})
	env.put("broken.yml", "broken: [puffin\n")
	if hits := env.search("puffin", QueryOpts{}).Hits; len(hits) != 1 || hits[0].Breadcrumb != "broken" {
		t.Fatalf("invalid YAML search hits = %+v", hits)
	}
}
