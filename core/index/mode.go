package index

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/parse/markdown"
	"github.com/yongjohnlee80/golib/search/chunk"
)

// Mode is how one workspace path is indexed beyond include and exclude (ADR 1792015212).
type Mode struct {
	// GraphOnly indexes the document's row, frontmatter, links and relations, and no chunks: it is
	// never a lexical or semantic hit, and never embedded.
	GraphOnly bool
	// Store is the todo store the path lies in, nil for none: its tasks answer to the store's names,
	// and their $VAR refs resolve through the store's variables.
	Store *TodoStore
}

// TodoStore is a todo store as the index reads its documents (ADR 1792015212 §2.4).
type TodoStore struct {
	// ID is the store's stable id in the todo registry: a task answers to "#todo:<ID>/<task id>",
	// so two stores' identical ids never meet.
	ID string
	// Vars are the store's variables' values (ADR 1792015069), by name; a name with no value here
	// is left out. A $VAR ref of a task resolves through these, never the workspace root.
	Vars map[string]string
}

// version is what m adds to a document's indexer version: a path whose mode changes is indexed
// again, its bytes unchanged, and so is a task whose store's variables change.
func (m Mode) version() string {
	var b strings.Builder
	if m.GraphOnly {
		b.WriteString(";graph")
	}
	if m.Store != nil {
		b.WriteString(";todo=" + m.Store.ID + ":" + m.Store.fingerprint())
	}
	return b.String()
}

// fingerprint is a short hash of the store's variables, names in order.
func (t *TodoStore) fingerprint() string {
	names := make([]string, 0, len(t.Vars))
	for n := range t.Vars {
		names = append(names, n)
	}
	slices.Sort(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n + "=" + t.Vars[n] + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// mode is path p's mode: the workspace's Modes, or full and in no store without them.
func (x *Indexer) mode(p string) Mode {
	if x.opts.Modes == nil {
		return Mode{}
	}
	return x.opts.Modes(p)
}

// todoPrefix starts the name a task answers to in its store.
const todoPrefix = "#todo:"

// todoKey is the name task id of store answers to.
func todoKey(store, id string) string { return todoPrefix + store + "/" + id }

// taskID is the task id a store's file answers to: its file name without ".md".
func taskID(p string) string { return strings.TrimSuffix(path.Base(p), ".md") }

// externalPrefix starts the name of a link to a file outside the workspace: no document answers to
// it, so it never resolves inside the index.
const externalPrefix = "#file:"

// storeRelations are a task's relations that only a store's documents make: "blocked" names other
// tasks of the store by id.
var storeRelations = []string{"blocked"}

// taskRelations reads the relations of file p, a task of store t, whose index root is root
// (absolute; "" for none known). They are read as any document's are, except:
//   - a $VAR path resolves through the store's variables: inside the workspace it is looked up by
//     its workspace path, outside it is an external link, and an unknown or unset variable is
//     unresolved rather than read from the workspace root;
//   - an absolute path is a workspace path, or an external link;
//   - "blocked" names a task of the same store, by id.
func taskRelations(doc *markdown.Document, p string, t *TodoStore, root string) []DocLink {
	var out []DocLink
	for _, r := range chunk.Relations(doc, RelationFields) {
		ref := chunk.ParseRef(r.Raw)
		var abs string
		switch {
		case ref.Var != "":
			v := t.Vars[ref.Var]
			if v == "" {
				// unresolved: the variable's name keeps it from naming any document
				out = append(out, DocLink{Raw: r.Raw, Name: "$" + ref.Var + "/" + ref.Target, Anchor: ref.Anchor,
					Kind: r.Field, Keys: []string{"$" + ref.Var + "/" + ref.Target}})
				continue
			}
			abs = filepath.Join(v, ref.Target)
		case ref.Form == chunk.RefPath && strings.HasPrefix(ref.Target, "/"):
			abs = filepath.Clean(ref.Target)
		default:
			out = append(out, relationsOf(p, r, ref)...)
			continue
		}
		if rel, ok := inside(root, abs); ok {
			k := exactName(wikiName(rel))
			out = append(out, DocLink{Raw: r.Raw, Name: k, Anchor: ref.Anchor, Kind: r.Field, Keys: []string{k}})
			continue
		}
		out = append(out, DocLink{Raw: r.Raw, Name: externalPrefix + abs, Anchor: ref.Anchor, Kind: r.Field, Target: abs})
	}
	for _, r := range chunk.Relations(doc, storeRelations) {
		id := strings.TrimSpace(r.Raw)
		if id == "" || strings.ContainsAny(id, " \t/") {
			continue // prose names no task
		}
		k := todoKey(t.ID, id)
		out = append(out, DocLink{Raw: r.Raw, Name: k, Kind: r.Field, Keys: []string{k}})
	}
	return out
}

// inside is abs as a path under root ("" never holds it), slash-separated.
func inside(root, abs string) (string, bool) {
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
