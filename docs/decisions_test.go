package docs_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The repository is public; the records its decisions were first written in are not. A decision
// the code relies on is described in docs/decisions/, and the code points there
// (docs/decisions/<page>.md#<decision>), never at a record a reader can't open.

// adrID is a decision record's number, as the code once cited them; adrIDSplit is one whose number
// a line break separated from "ADR", the comment marker of the next line between them.
var (
	adrID      = regexp.MustCompile(`ADR[- ]?([0-9]{4,})`)
	adrIDSplit = regexp.MustCompile(`ADR[- ]?[ \t]*\n[ \t]*(?://+|-{2,3}|#+|\*|;+)?[ \t]*([0-9]{4,})`)
)

// releasedScripts are the schema scripts released with record numbers in their comments, and how
// many each holds. A released script is immutable (sql/deployments' TestReleasedScriptsAreUnchanged
// pins its digest), so these stay as they are; they are bare numbers, with no text. A new script,
// or another number in one of these, is refused like any other file.
var releasedScripts = map[string]int{
	"sql/deployments/postgres/000001_update_initialize_tables.sql":          1,
	"sql/deployments/postgres/000002_revert_add_preferences.sql":            1,
	"sql/deployments/postgres/000002_update_add_preferences.sql":            1,
	"sql/deployments/postgres/000003_revert_provider_context.sql":           1,
	"sql/deployments/postgres/000003_update_provider_context.sql":           1,
	"sql/deployments/postgres/000004_revert_embedding_model_lookup.sql":     1,
	"sql/deployments/postgres/000004_update_embedding_model_lookup.sql":     1,
	"sql/deployments/postgres/000005_revert_workspace_embedding_policy.sql": 1,
	"sql/deployments/postgres/000005_update_workspace_embedding_policy.sql": 1,
	"sql/deployments/postgres/000006_revert_empty_include.sql":              1,
	"sql/deployments/postgres/000006_update_empty_include.sql":              1,
	"sql/deployments/postgres/000007_revert_frontmatter_schema.sql":         1,
	"sql/deployments/postgres/000007_update_frontmatter_schema.sql":         1,
	"sql/deployments/postgres/000008_revert_text_extensions.sql":            1,
	"sql/deployments/postgres/000008_update_text_extensions.sql":            1,
	"sql/deployments/postgres/000009_revert_events.sql":                     1,
	"sql/deployments/postgres/000009_update_events.sql":                     1,
	"sql/deployments/postgres/000010_revert_workspace_provider.sql":         1,
	"sql/deployments/postgres/000010_update_workspace_provider.sql":         1,
	"sql/deployments/postgres/000011_revert_workspace_databases.sql":        1,
	"sql/deployments/postgres/000011_update_workspace_databases.sql":        1,
	"sql/deployments/postgres/000012_revert_chunk_embed.sql":                1,
	"sql/deployments/postgres/000012_update_chunk_embed.sql":                1,
	"sql/deployments/postgres/000013_revert_rankers.sql":                    1,
	"sql/deployments/postgres/000013_update_rankers.sql":                    1,
	"sql/deployments/postgres/000014_revert_relation_keys.sql":              1,
	"sql/deployments/postgres/000014_update_relation_keys.sql":              1,
	"sql/deployments/postgres/000015_revert_model_target.sql":               1,
	"sql/deployments/postgres/000015_update_model_target.sql":               1,
	"sql/deployments/postgres/000016_revert_chunk_text.sql":                 1,
	"sql/deployments/postgres/000016_update_chunk_text.sql":                 1,
	"sql/deployments/postgres/000017_revert_todo_journal.sql":               1,
	"sql/deployments/postgres/000017_update_todo_journal.sql":               1,
	"sql/deployments/postgres/000018_revert_todo_consumer_resync.sql":       1,
	"sql/deployments/postgres/000018_update_todo_consumer_resync.sql":       1,
	"sql/deployments/postgres/000019_revert_todo_store_refreshed.sql":       1,
	"sql/deployments/postgres/000019_update_todo_store_refreshed.sql":       1,
	"sql/deployments/postgres/000020_revert_todo_store_vars.sql":            1,
	"sql/deployments/postgres/000020_update_todo_store_vars.sql":            1,
	"sql/deployments/postgres/000021_revert_todo_graph_only.sql":            1,
	"sql/deployments/postgres/000021_update_todo_graph_only.sql":            1,
	"sql/deployments/postgres/000022_revert_todo_search_default.sql":        1,
	"sql/deployments/postgres/000022_update_todo_search_default.sql":        1,
	"sql/deployments/postgres/000023_revert_workspace_vars.sql":             1,
	"sql/deployments/postgres/000023_update_workspace_vars.sql":             1,
	"sql/deployments/postgres/000024_revert_todo_store_released.sql":        1,
	"sql/deployments/postgres/000024_update_todo_store_released.sql":        1,
	"sql/deployments/postgres/000025_revert_todo_assign_scope.sql":          1,
	"sql/deployments/postgres/000025_update_todo_assign_scope.sql":          1,
	"sql/deployments/postgres/000026_update_crew_mail_inbox.sql":            2,
	"sql/deployments/postgres/000027_update_document_sort_keys.sql":         2,
	"sql/deployments/sqlite/000007_update_frontmatter_schema.sql":           1,
	"sql/deployments/sqlite/000008_update_text_extensions.sql":              1,
	"sql/deployments/sqlite/000009_update_events.sql":                       1,
	"sql/deployments/sqlite/000010_update_workspace_provider.sql":           1,
	"sql/deployments/sqlite/000011_update_workspace_databases.sql":          1,
	"sql/deployments/sqlite/000012_update_chunk_embed.sql":                  2,
	"sql/deployments/sqlite/000013_update_rankers.sql":                      1,
	"sql/deployments/sqlite/000015_update_model_target.sql":                 1,
	"sql/deployments/sqlite/000016_update_chunk_text.sql":                   1,
	"sql/deployments/sqlite/000017_update_todo_journal.sql":                 1,
	"sql/deployments/sqlite/000018_update_todo_consumer_resync.sql":         1,
	"sql/deployments/sqlite/000019_update_todo_store_refreshed.sql":         1,
	"sql/deployments/sqlite/000020_update_todo_store_vars.sql":              1,
	"sql/deployments/sqlite/000021_update_todo_graph_only.sql":              1,
	"sql/deployments/sqlite/000022_update_todo_search_default.sql":          1,
	"sql/deployments/sqlite/000023_update_workspace_vars.sql":               1,
	"sql/deployments/sqlite/000024_update_todo_store_released.sql":          1,
	"sql/deployments/sqlite/000025_update_todo_assign_scope.sql":            1,
	"sql/deployments/sqlite/000026_update_crew_mail_inbox.sql":              1,
	"sql/deployments/sqlite/000027_update_document_sort_keys.sql":           1,
}

// examples are the files where a record number is the product's own example: AutoDoc indexes ADRs
// as a document type, and these show sample ones. Each names the sample numbers it may show.
var examples = map[string][]string{
	"AGENTS.md": {"0007"},
	"docs/reference/05-hybrid-search-pipeline.md":                                         {"0007"},
	"core/todo/worktree_test.go":                                                          {"0158"},
	"docs/reference/07-frontmatter-schemas-and-validation.md":                             {"0220"},
	"docs/reference/08-knowledge-graphs-and-adr-lifecycle.md":                             {"0001", "0009", "0014"},
	"docs/reference/08-knowledge-graphs-and-adr-lifecycle/adr-supersession-lifecycle.mmd": {"0001", "0009", "0014"},
	"tests/kb_spec.lua": {"0001", "0002"},
}

// repoFiles walks the repository's text files from root, skipping what is not the project's
// own (.git, build output, dependencies) and binary files, and calls fn with each one's
// slash-separated path from root and its content.
func repoFiles(t *testing.T, fn func(path string, b []byte)) {
	t.Helper()
	root := ".."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "node_modules", ".autodoc-test-logs":
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
			return nil // binary
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fn(filepath.ToSlash(rel), b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNoFileCitesAPrivateDecisionRecord: no file of the repository cites a decision record by its
// number; the decision is described in docs/decisions/ and pointed at there. The exemptions are
// positive: the released schema scripts, each with the count it shipped with, and the product's
// example files, each with the sample numbers it shows.
func TestNoFileCitesAPrivateDecisionRecord(t *testing.T) {
	seen := map[string]int{}
	repoFiles(t, func(path string, b []byte) {
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range adrID.FindAllStringSubmatch(line, -1) {
				if _, ok := releasedScripts[path]; ok {
					seen[path]++
					continue
				}
				if ids, ok := examples[path]; ok && slices.Contains(ids, m[1]) {
					continue
				}
				t.Errorf("%s:%d: %q cites a decision record: describe the decision in docs/decisions/ and point there", path, i+1, m[0])
			}
		}
		for _, m := range adrIDSplit.FindAllSubmatchIndex(b, -1) {
			id := string(b[m[2]:m[3]])
			if ids, ok := examples[path]; ok && slices.Contains(ids, id) {
				continue
			}
			line := 1 + bytes.Count(b[:m[0]], []byte("\n"))
			t.Errorf("%s:%d: \"ADR\" and %s across a line break cite a decision record: describe the decision in docs/decisions/ and point there", path, line, id)
		}
	})
	for path, want := range releasedScripts {
		if got := seen[path]; got != want {
			t.Errorf("%s: %d record numbers, want the %d it was released with", path, got, want)
		}
	}
}

// pointer is a pointer at a decision, from code (docs/decisions/<page>.md#<decision>) or a page
// beside it (../decisions/<page>.md#<decision>): its page and its decision's anchor.
var pointer = regexp.MustCompile(`decisions/([a-z0-9-]+\.md)#([a-z0-9-]+)`)

// TestEveryDecisionPointerResolves: every decisions/<page>.md#<anchor> in the repository names a
// page that exists and a heading on it: a pointer a reader follows reaches the decision.
func TestEveryDecisionPointerResolves(t *testing.T) {
	anchors := map[string]map[string]bool{}
	pages, err := filepath.Glob("decisions/*.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		set := map[string]bool{}
		for _, line := range strings.Split(string(b), "\n") {
			if h, ok := strings.CutPrefix(line, "## "); ok {
				set[slug(h)] = true
			}
		}
		anchors[filepath.Base(p)] = set
	}
	n := 0
	repoFiles(t, func(path string, b []byte) {
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range pointer.FindAllStringSubmatch(line, -1) {
				n++
				set, ok := anchors[m[1]]
				switch {
				case !ok:
					t.Errorf("%s:%d: %s: no such page in docs/decisions/", path, i+1, m[0])
				case !set[m[2]]:
					t.Errorf("%s:%d: %s: no heading %q on %s", path, i+1, m[0], m[2], m[1])
				}
			}
		}
	})
	if n == 0 {
		t.Error("no pointer at docs/decisions/ was found: the walk read nothing")
	}
}

// slug is a heading's anchor as GitHub makes it: lower case, punctuation but hyphens dropped,
// spaces to hyphens.
func slug(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(h)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}
