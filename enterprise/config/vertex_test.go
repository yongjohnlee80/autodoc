package config_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
)

// vertexEmbedding is the good file with its [embedding] vertex's.
func vertexEmbedding(t *testing.T, body string) string {
	t.Helper()
	start, end := strings.Index(good, "[embedding]"), strings.Index(good, "[http]")
	if start < 0 || end < start {
		t.Fatal("the good file has no [embedding] before [http]")
	}
	return good[:start] + "[embedding]\n" + body + "\n" + good[end:]
}

const vertexBody = `provider   = "vertex"
project    = "my-project"
location   = "us"
model      = "gemini-embedding-2"
dimensions = 1536`

// vertex's [embedding] and [ranker] load: no key, one text a call, Google's hosts by default.
func TestVertexLoads(t *testing.T) {
	s, err := load(t, vertexEmbedding(t, vertexBody)+"\n[ranker]\nkind = \"vertex\"\nproject = \"my-project\"\nmodel = \"semantic-ranker-default-005\"\n")
	if err != nil {
		t.Fatal(err)
	}
	e := s.Embedding
	if e.Provider != "vertex" || e.Project != "my-project" || e.Location != "us" || e.Dimensions != 1536 || e.Batch != 1 || e.KeyEnv != "" || e.BaseURL != "" {
		t.Errorf("embedding %+v", e)
	}
	if r := s.Ranker; r == nil || r.Kind != "vertex" || r.Project != "my-project" || r.Model != "semantic-ranker-default-005" || r.BaseURL != "" {
		t.Errorf("ranker %+v", r)
	}
}

// Each vertex refusal names its field.
func TestEveryVertexRefusalNamesItsField(t *testing.T) {
	sub := func(old, new string) string { return vertexEmbedding(t, strings.Replace(vertexBody, old, new, 1)) }
	rank := func(body string) string { return vertexEmbedding(t, vertexBody) + "\n[ranker]\n" + body + "\n" }
	for _, c := range []struct{ name, src, field, says string }{
		{"no project", sub(`project    = "my-project"`, ""), "embedding.project", "required for vertex"},
		{"no location", sub(`location   = "us"`, ""), "embedding.location", "required for vertex"},
		{"a region", sub(`location   = "us"`, `location = "us-central1"`), "embedding.location", `want "global", "us" or "eu"`},
		{"another model", sub(`model      = "gemini-embedding-2"`, `model = "text-embedding-005"`), "embedding.model", "gemini-embedding-2"},
		{"too few dimensions", sub(`dimensions = 1536`, `dimensions = 127`), "embedding.dimensions", "want 128 to 3072"},
		{"too many dimensions", sub(`dimensions = 1536`, `dimensions = 3073`), "embedding.dimensions", "want 128 to 3072"},
		{"a key", sub(`dimensions = 1536`, "dimensions = 1536\nkey_env = \"TEST_EMBED_KEY\""), "embedding.key_env", "Application Default Credentials"},
		{"a batch", sub(`dimensions = 1536`, "dimensions = 1536\nbatch = 2"), "embedding.batch", "batch is 1"},
		{"a project for openai", strings.Replace(good, `slots    = 4`, "slots    = 4\nproject = \"p\"", 1), "embedding.project", "vertex's only"},
		{"dimensions for openai", strings.Replace(good, `slots    = 4`, "slots    = 4\ndimensions = 768", 1), "embedding.dimensions", "vertex's only"},
		{"a ranker with no project", rank(`kind = "vertex"` + "\nmodel = \"semantic-ranker-default-005\""), "ranker.project", "required for vertex"},
		{"a ranker with no model", rank(`kind = "vertex"` + "\nproject = \"p\""), "ranker.model", "required for vertex"},
		{"a ranker on an alias", rank(`kind = "vertex"` + "\nproject = \"p\"\nmodel = \"semantic-ranker-default@latest\""), "ranker.model", "pin a version"},
		{"a ranker with a key", rank(`kind = "vertex"` + "\nproject = \"p\"\nmodel = \"semantic-ranker-default-005\"\nkey_env = \"TEST_EMBED_KEY\""), "ranker.key_env", "Application Default Credentials"},
		{"a project for tei", rank(`kind = "tei"` + "\nbase_url = \"http://127.0.0.1:8081\"\nproject = \"p\""), "ranker.project", "vertex's only"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, c.src)
			var r *config.Refusal
			if !errors.As(err, &r) {
				t.Fatalf("Load: %v, want a refusal", err)
			}
			i := slices.Index(r.Fields(), c.field)
			if i < 0 || !strings.Contains(r.Errors[i].Reason, c.says) {
				t.Fatalf("refused %v (%v), want %s saying %q", r.Fields(), err, c.field, c.says)
			}
		})
	}
}
