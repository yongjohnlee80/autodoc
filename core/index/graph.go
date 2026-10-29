package index

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/graph"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// ErrNoDocument is a path the index does not hold.
var ErrNoDocument = errs.Sentinel(errs.ErrInvalidArgument, "index: no such document")

// Link is one link of graph.links or graph.backlinks (ADR 0204 §4.6). Path is the other end: the
// target of a link, the source of a backlink; "" for an unresolved link.
type Link struct {
	Path     string
	Raw      string // the link as written
	Anchor   string // the heading, or "^" and the block id; "" for none
	Kind     string // LinkWikilink, LinkEmbed or LinkMarkdown, or a relation's field
	Resolved bool
	// External is a link's file outside the workspace, absolute: a todo task's ref (ADR 1792015212
	// §2.4). It reaches no document, and is not unresolved. "" for every other link.
	External string
}

// Edge is a resolved link between two documents of a neighbourhood.
type Edge struct {
	Src, Dst, Kind string
}

// Neighborhood is graph.neighborhood: the documents within depth links of one, either way, and the
// resolved links among them.
type Neighborhood struct {
	Nodes []string
	Edges []Edge
}

// Unresolved is one link of graph.unresolved: its source, as written, and why it reaches nothing
// (ReasonMissing or ReasonAmbiguous).
type Unresolved struct {
	Src, Raw, Reason string
}

// kindsIn narrows a link query to links of the given kinds; nil leaves it every kind. The filter is
// in the query, so a walk never passes through a link of a kind it was not given.
func kindsIn(q dao.DAO[*LinkRow, LinkField, int64], kinds []string) dao.DAO[*LinkRow, LinkField, int64] {
	if kinds == nil {
		return q
	}
	vs := make([]any, len(kinds))
	for i, k := range kinds {
		vs[i] = k
	}
	return q.WithPredicate(dao.In(`"link"."kind"`, vs))
}

// Links lists the links of the document at path, in source order: those of the given kinds, every
// kind when kinds is nil.
func (s *Store) Links(ctx context.Context, path string, kinds []string) ([]Link, error) {
	out := []Link{}
	err := s.read(ctx, func(tx *store.Tx) error {
		id, err := s.docID(tx, path)
		if err != nil {
			return err
		}
		rows, err := kindsIn(s.linksOut(tx), kinds).With(LinkSrc, id).OrderBy(dao.Asc(LinkByID)).
			Select(LinkOtherPath, LinkRaw, LinkAnchor, LinkKind, LinkDst, LinkTarget)
		for _, r := range rows {
			out = append(out, Link{Path: r.OtherPath, Raw: r.Raw, Anchor: deref(r.Anchor), Kind: r.Kind, Resolved: r.DstDoc != nil,
				External: deref(r.Target)})
		}
		return err
	})
	return out, err
}

// Backlinks lists the links of the given kinds (nil: every kind) that reach the document at path,
// by source path, then source order.
func (s *Store) Backlinks(ctx context.Context, path string, kinds []string) ([]Link, error) {
	out := []Link{}
	err := s.read(ctx, func(tx *store.Tx) error {
		id, err := s.docID(tx, path)
		if err != nil {
			return err
		}
		rows, err := kindsIn(s.linksIn(tx), kinds).With(LinkDst, id).OrderBy(dao.Asc(LinkByOtherPath)).
			Select(LinkOtherPath, LinkRaw, LinkAnchor, LinkKind)
		for _, r := range rows {
			out = append(out, Link{Path: r.OtherPath, Raw: r.Raw, Anchor: deref(r.Anchor), Kind: r.Kind, Resolved: true})
		}
		return err
	})
	return out, err
}

// MapNode is a document of graph.map: its path, its title, its frontmatter "type" ("" when none),
// whether another document supersedes it, and its tags (sorted; nil for none).
type MapNode struct {
	Path, Title, Type string
	Superseded        bool
	Tags              []string
}

// Map is graph.map: documents and the resolved links among them, both sorted.
type Map struct {
	Nodes []MapNode
	Edges []Edge
}

// MapOpts selects graph.map's documents: every document of the workspace, or with Root those within
// Depth (1 or 2; others are clamped) resolved links of it. Kinds are the links followed and returned
// (nil: every kind), as Neighborhood takes them.
type MapOpts struct {
	Root  string
	Depth int
	Kinds []string
	// TypeField is the frontmatter field a node's Type is read from: the schema's discriminator;
	// "" is "type".
	TypeField string
}

// Map gathers graph.map. The workspace's resolved links of the given kinds are read as a
// graph.Graph. With a root, the subgraph within depth of it, both ways, as Neighborhood walks it;
// without, every document, linked or not, and every one of those links. Each node's Superseded
// comes from every resolved supersedes and superseded_by link, whatever kinds were asked for, so a
// filter never unmarks a document; a document naming itself is not superseded by it.
func (s *Store) Map(ctx context.Context, o MapOpts) (Map, error) {
	return s.graphMap(ctx, o, o.Root != "")
}

// graphMap is Map, rooted at o.Root when rooted: an empty or unknown root is then ErrNoDocument,
// as Neighborhood has always answered; unrooted, o.Root is unused.
//
// Rooted, it reads only the neighbourhood (AP-6): the links of the frontier, both ways, one step
// at a time, then the links among the documents reached, and those documents' rows, tags and
// supersession; never the workspace's every link, tag and supersession, which a rooted map of a
// few documents used to read whole. Its answer is golib's graph.Neighborhood's over the whole
// graph: every document within depth links of the root either way, and every link of the given
// kinds whose two ends were both reached.
func (s *Store) graphMap(ctx context.Context, o MapOpts, rooted bool) (Map, error) {
	var m Map
	err := s.read(ctx, func(tx *store.Tx) error {
		var ids []int64 // the documents of the map; nil: every one
		var links []*LinkRow
		if rooted {
			start, err := s.docID(tx, o.Root)
			if err != nil {
				return err
			}
			if ids, links, err = s.neighbourhood(tx, start, min(max(o.Depth, 1), 2), o.Kinds); err != nil {
				return err
			}
		} else {
			var err error
			if links, err = kindsIn(s.linksOut(tx), o.Kinds).WithPredicate(dao.IsNotNull(`"link"."dst_doc"`)).
				Select(LinkSrc, LinkDst, LinkKind); err != nil {
				return err
			}
		}
		found, err := s.docsByID(tx, ids)
		if err != nil {
			return err
		}
		superseded, err := s.superseded(tx, ids)
		if err != nil {
			return err
		}
		tags, err := s.tagsByDoc(tx, ids)
		if err != nil {
			return err
		}
		paths := map[int64]string{}
		for _, d := range found {
			paths[d.ID] = d.Path
			n := MapNode{Path: d.Path, Type: frontmatterType(d.FrontmatterJSON, cmp.Or(o.TypeField, "type")), Superseded: superseded[d.ID], Tags: tags[d.ID]}
			if d.Title != nil {
				n.Title = *d.Title
			}
			m.Nodes = append(m.Nodes, n)
		}
		g := graph.New[int64]() // a link repeated (one document linking another twice) is one edge
		for _, l := range links {
			g.Add(graph.Edge[int64]{Src: l.SrcDoc, Dst: *l.DstDoc, Kind: l.Kind})
		}
		for e := range g.Edges(graph.Filter{}) {
			m.Edges = append(m.Edges, Edge{paths[e.Src], paths[e.Dst], e.Kind})
		}
		return nil
	})
	if err != nil {
		return Map{}, err
	}
	sort.Slice(m.Nodes, func(i, j int) bool { return m.Nodes[i].Path < m.Nodes[j].Path })
	sort.Slice(m.Edges, func(i, j int) bool {
		a, b := m.Edges[i], m.Edges[j]
		if a.Src != b.Src {
			return a.Src < b.Src
		}
		if a.Dst != b.Dst {
			return a.Dst < b.Dst
		}
		return a.Kind < b.Kind
	})
	return m, nil
}

// neighbourhood walks the resolved links of the given kinds from start, both ways, depth steps: the
// documents reached (start first), and the links among them. Each step reads only its frontier's
// links, through link_src and link_dst, in parts small enough for one IN list.
func (s *Store) neighbourhood(tx *store.Tx, start int64, depth int, kinds []string) ([]int64, []*LinkRow, error) {
	resolved := func() dao.DAO[*LinkRow, LinkField, int64] {
		return kindsIn(s.linksOut(tx), kinds).WithPredicate(dao.IsNotNull(`"link"."dst_doc"`))
	}
	reached := map[int64]bool{start: true}
	ids := []int64{start}
	frontier := []int64{start}
	for step := 0; step < depth && len(frontier) > 0; step++ {
		var next []int64
		visit := func(id int64) {
			if !reached[id] {
				reached[id] = true
				next = append(next, id)
			}
		}
		for part := range inParts(frontier) {
			out, err := resolved().With(LinkSrc, part...).Select(LinkDst)
			if err != nil {
				return nil, nil, err
			}
			for _, l := range out {
				visit(*l.DstDoc)
			}
			in, err := resolved().With(LinkDst, part...).Select(LinkSrc)
			if err != nil {
				return nil, nil, err
			}
			for _, l := range in {
				visit(l.SrcDoc)
			}
		}
		ids = append(ids, next...)
		frontier = next
	}
	var links []*LinkRow
	for part := range inParts(ids) {
		rows, err := resolved().With(LinkSrc, part...).Select(LinkSrc, LinkDst, LinkKind)
		if err != nil {
			return nil, nil, err
		}
		for _, l := range rows {
			if reached[*l.DstDoc] {
				links = append(links, l)
			}
		}
	}
	return ids, links, nil
}

// docsByID is the rows graph.map shows of the documents ids, in parts; every document when ids is
// nil.
func (s *Store) docsByID(tx *store.Tx, ids []int64) ([]*Document, error) {
	if ids == nil {
		return s.documents(tx).Select(DocID, DocPath, DocTitle, DocFrontmatterJSON)
	}
	var out []*Document
	for part := range inParts(ids) {
		rows, err := s.documents(tx).With(DocID, part...).Select(DocID, DocPath, DocTitle, DocFrontmatterJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// tagsByDoc is the tags of the documents ids, sorted, by document; every document's when ids is
// nil.
func (s *Store) tagsByDoc(tx *store.Tx, ids []int64) (map[int64][]string, error) {
	var rows []*DocValue
	if ids == nil {
		var err error
		if rows, err = s.tags(tx).Select(DocValueDoc, DocValueValue); err != nil {
			return nil, err
		}
	}
	for part := range inParts(ids) {
		r, err := s.tags(tx).With(DocValueDoc, part...).Select(DocValueDoc, DocValueValue)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r...)
	}
	out := map[int64][]string{}
	for _, r := range rows {
		out[r.DocID] = append(out[r.DocID], r.Value)
	}
	for _, t := range out {
		sort.Strings(t)
	}
	return out, nil
}

// superseded is the documents among ids (every document when ids is nil) that another one
// supersedes, by the resolved supersession links: a supersedes link's target, and a superseded_by
// link's source.
func (s *Store) superseded(tx *store.Tx, ids []int64) (map[int64]bool, error) {
	supersession := func() dao.DAO[*LinkRow, LinkField, int64] {
		return kindsIn(s.linksOut(tx), []string{"supersedes", "superseded_by"}).WithPredicate(dao.IsNotNull(`"link"."dst_doc"`))
	}
	var rows []*LinkRow
	if ids == nil {
		var err error
		if rows, err = supersession().Select(LinkSrc, LinkDst, LinkKind); err != nil {
			return nil, err
		}
	}
	for part := range inParts(ids) {
		// a document is superseded by a supersedes link reaching it or a superseded_by link leaving it
		to, err := supersession().With(LinkDst, part...).Select(LinkSrc, LinkDst, LinkKind)
		if err != nil {
			return nil, err
		}
		from, err := supersession().With(LinkSrc, part...).Select(LinkSrc, LinkDst, LinkKind)
		if err != nil {
			return nil, err
		}
		rows = append(append(rows, to...), from...)
	}
	out := map[int64]bool{}
	for _, l := range rows {
		if l.SrcDoc == *l.DstDoc {
			continue // a document naming itself supersedes nothing
		}
		if l.Kind == "supersedes" {
			out[*l.DstDoc] = true
		} else {
			out[l.SrcDoc] = true
		}
	}
	return out, nil
}

// frontmatterType is a document's frontmatter "type", "" when it has none or it is not a string.
func frontmatterType(raw *string, field string) string {
	if raw == nil || *raw == "" {
		return ""
	}
	var front map[string]any
	if json.Unmarshal([]byte(*raw), &front) != nil {
		return ""
	}
	t, _ := front[field].(string)
	return t
}

// Neighborhood gathers the documents within depth (1 or 2; others are clamped) resolved links of
// the one at path, following links of the given kinds (nil: every kind) both ways, and those links
// among them: graph.map's rooted walk, as paths. A path that names no document, the empty one
// included, is ErrNoDocument: unlike Map, Neighborhood always has a root.
func (s *Store) Neighborhood(ctx context.Context, path string, depth int, kinds []string) (Neighborhood, error) {
	m, err := s.graphMap(ctx, MapOpts{Root: path, Depth: depth, Kinds: kinds}, true)
	if err != nil {
		return Neighborhood{}, err
	}
	nb := Neighborhood{Edges: m.Edges}
	for _, n := range m.Nodes {
		nb.Nodes = append(nb.Nodes, n.Path)
	}
	return nb, nil
}

// Unresolved lists every link of the given kinds (nil: every kind) that reaches no document, by
// source path, then source order; then, when the kinds take in supersession, the supersession
// relations that go round a loop (ReasonCycle), which resolve but have no order.
func (s *Store) Unresolved(ctx context.Context, kinds []string) ([]Unresolved, error) {
	out := []Unresolved{}
	err := s.read(ctx, func(tx *store.Tx) error {
		// an external link reaches a file outside the workspace: it is where it should be
		rows, err := kindsIn(s.linksIn(tx), kinds).WithPredicate(dao.IsNull(`"link"."dst_doc"`)).
			WithPredicate(dao.IsNull(`"link"."target"`)).OrderBy(dao.Asc(LinkByOtherPath)).
			Select(LinkID, LinkOtherPath, LinkRaw, LinkKind, LinkName)
		if err != nil {
			return err
		}
		for _, r := range rows {
			// the reason is not stored: resolving again, in this snapshot, gives the writer's answer
			_, reason, err := s.resolveStored(tx, r.ID, r.Kind, r.Name)
			if err != nil {
				return err
			}
			out = append(out, Unresolved{Src: r.OtherPath, Raw: r.Raw, Reason: reason})
		}
		if kinds != nil && !slices.Contains(kinds, kindSupersedes) && !slices.Contains(kinds, kindSupersededBy) {
			return nil
		}
		loops, err := s.supersessionLoops(tx, kinds)
		out = append(out, loops...)
		return err
	})
	return out, err
}

// supersessionLoops lists the supersession relations that go round a loop (A superseded by B, B by
// A): each resolves, so none is unresolved, yet a loop has no order, and the demotion leaves its
// documents where they are. A relation is on a loop when its successor leads back to the document
// it replaces. Listed by source path, then source order, with ReasonCycle: of the given kinds only
// (nil: both), though a loop is found through either.
func (s *Store) supersessionLoops(tx *store.Tx, kinds []string) ([]Unresolved, error) {
	rows, err := kindsIn(s.linksIn(tx), []string{kindSupersedes, kindSupersededBy}).
		WithPredicate(dao.IsNotNull(`"link"."dst_doc"`)).OrderBy(dao.Asc(LinkByOtherPath), dao.Asc(LinkByID)).
		Select(LinkSrc, LinkDst, LinkKind, LinkRaw, LinkOtherPath)
	if err != nil {
		return nil, err
	}
	// every relation as "replaced -> successor", whichever side wrote it
	g := graph.New[int64]()
	edge := func(src, dst int64, kind string) (old, successor int64) {
		if kind == kindSupersededBy {
			return src, dst
		}
		return dst, src
	}
	for _, r := range rows {
		old, successor := edge(r.SrcDoc, *r.DstDoc, r.Kind)
		g.Add(graph.Edge[int64]{Src: old, Dst: successor, Kind: kindSupersededBy})
	}
	var out []Unresolved
	for _, r := range rows {
		old, successor := edge(r.SrcDoc, *r.DstDoc, r.Kind)
		// the loop is found through both kinds; a row is listed only when its own kind was asked for
		if (kinds == nil || slices.Contains(kinds, r.Kind)) && old != successor &&
			g.Neighborhood(successor, len(rows), graph.Out, graph.Filter{}).Has(old) {
			out = append(out, Unresolved{Src: r.OtherPath, Raw: r.Raw, Reason: ReasonCycle})
		}
	}
	return out, nil
}

func (s *Store) docID(tx *store.Tx, path string) (int64, error) {
	d, err := s.documents(tx).With(DocPath, path).Get(DocID)
	if errors.Is(err, dao.ErrNoRows) {
		return 0, ErrNoDocument
	}
	if err != nil {
		return 0, err
	}
	return d.ID, nil
}
