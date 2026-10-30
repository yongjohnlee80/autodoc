package index

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/parse/markdown"
	"github.com/yongjohnlee80/golib/search/chunk"

	"github.com/yongjohnlee80/autodoc/core/schema"
)

// Mode is how one workspace path is indexed beyond include and exclude
// (docs/decisions/todos.md#graph-only-indexing).
type Mode struct {
	// GraphOnly indexes the document's row, frontmatter, links and relations, and no chunks: it is
	// never a lexical or semantic hit, and never embedded.
	GraphOnly bool
	// Store is the todo store the path lies in, nil for none: its tasks answer to the store's names.
	Store *TodoStore
	// Vars are the variables of the workspace the path belongs to
	// (docs/decisions/todos.md#resolving-refs-in-every-document): a $NAME ref
	// of the document resolves through them. nil when the index is given none: a task's $NAME ref is
	// then unresolved, and any other document's reads its root as the workspace root, as before.
	Vars *Vars
}

// TodoStore is a todo store as the index reads its documents
// (docs/decisions/todos.md#task-relations-and-external-links).
type TodoStore struct {
	// ID is the store's stable id in the todo registry: a task answers to "#todo:<ID>/<task id>",
	// so two stores' identical ids never meet.
	ID string
	// Check is how the todo format judges a task file's frontmatter (core/todo's CheckFile, the
	// daemon's): a task is never checked against the workspace's KB schema
	// (docs/decisions/todos.md#tasks-are-checked-by-the-todo-format). nil checks it against the KB
	// schema, as a build without the todo system does.
	Check func(src []byte) schema.Result
}

// Vars are a workspace's variables' values, by name; a name with no value is left out. Make them
// with NewVars, once for every path they serve.
type Vars struct{ values map[string]string }

// NewVars are values as a mode's variables.
func NewVars(values map[string]string) *Vars {
	vs := make(map[string]string, len(values))
	for n, v := range values {
		if v != "" {
			vs[n] = v
		}
	}
	return &Vars{values: vs}
}

// Get is name's value, "" for none.
func (v *Vars) Get(name string) string {
	if v == nil {
		return ""
	}
	return v.values[name]
}

// version is what m adds to a document's indexer version: a path whose mode changes is indexed
// again, its bytes unchanged. The variables are not in it: a document's own are (varsVersion).
func (m Mode) version() string {
	var b strings.Builder
	if m.GraphOnly {
		b.WriteString(";graph")
	}
	if m.Store != nil {
		b.WriteString(";todo=" + m.Store.ID)
		if m.Store.Check != nil {
			b.WriteString(";fm=todo") // checked by the todo format: a task the KB schema checked is indexed again
		}
	}
	return b.String()
}

// varsVersion is what a document whose $NAME refs name names adds to its indexer version
// (docs/decisions/todos.md#resolving-refs-in-every-document): the names, and a fingerprint of their
// values in vars ("nil" for none given), so a change of the workspace's variables re-indexes
// exactly the documents that name a changed one. "" for a document that names none. A variable name
// holds no ',', ':' or ';'.
func varsVersion(names []string, vars *Vars) string {
	if len(names) == 0 {
		return ""
	}
	h := sha256.New()
	if vars == nil {
		h.Write([]byte("nil"))
	} else {
		for _, n := range names {
			h.Write([]byte(n + "=" + vars.Get(n) + "\n"))
		}
	}
	return ";vars=" + strings.Join(names, ",") + ":" + hex.EncodeToString(h.Sum(nil))[:12]
}

// varsNamed are the names a recorded indexer version's variables part names; nil for none.
func varsNamed(recorded string) []string {
	_, rest, ok := strings.Cut(recorded, ";vars=")
	if !ok {
		return nil
	}
	list, _, _ := strings.Cut(rest, ":")
	return strings.Split(list, ",")
}

// varNames are the variables a document's $NAME refs name, sorted: its relations' and its markdown
// links', the refs varRelations and varLink resolve.
func varNames(doc *markdown.Document) []string {
	seen := map[string]bool{}
	for _, r := range chunk.Relations(doc, RelationFields) {
		if v := chunk.ParseRef(r.Raw).Var; v != "" {
			seen[v] = true
		}
	}
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		for c := n.FirstChild; c != nil; c = c.Next {
			if c.Kind == markdown.KindLink && strings.HasPrefix(string(c.Dest()), "$") {
				dest, _, _ := strings.Cut(string(c.Dest()), "#")
				if d, err := url.PathUnescape(dest); err == nil {
					dest = d
				}
				if v := chunk.ParseRef(dest).Var; v != "" {
					seen[v] = true
				}
			}
			walk(c)
		}
	}
	walk(doc.Root)
	return slices.Sorted(maps.Keys(seen))
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

// varRelations reads the relations of file p, whose workspace's variables are vars and whose index
// root is root (absolute; "" for none known). They are read as extractRelations reads them, except:
//   - a $NAME path resolves through vars: inside the workspace it is looked up by its workspace
//     path, outside it is an external link, and an unknown or unset name is unresolved rather than
//     read from the workspace root;
//   - an absolute path is a workspace path, or an external link.
func varRelations(doc *markdown.Document, p string, vars *Vars, root string) []DocLink {
	var out []DocLink
	for _, r := range chunk.Relations(doc, RelationFields) {
		ref := chunk.ParseRef(r.Raw)
		var abs string
		switch {
		case ref.Var != "":
			v := vars.Get(ref.Var)
			if v == "" {
				out = append(out, unresolvedVar(r.Raw, ref, r.Field, true))
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
	return out
}

// unresolvedVar is a $NAME ref whose name has no value: the name keeps it from naming any document.
// A relation's names are its Keys; a body link has the one.
func unresolvedVar(raw string, ref chunk.Ref, kind string, relation bool) DocLink {
	name := "$" + ref.Var + "/" + ref.Target
	l := DocLink{Raw: raw, Name: name, Anchor: ref.Anchor, Kind: kind}
	if relation {
		l.Keys = []string{name}
	}
	return l
}

// taskRelations reads the relations of file p, a task of store t, as varRelations does, and its
// store's own: "blocked" names a task of the same store, by id.
func taskRelations(doc *markdown.Document, p string, t *TodoStore, vars *Vars, root string) []DocLink {
	out := varRelations(doc, p, vars, root)
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
