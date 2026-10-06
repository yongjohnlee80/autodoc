package index

import (
	"context"
	"errors"
	"sort"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/graph"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// ErrNoDocument is a path the index does not hold.
var ErrNoDocument = errors.New("index: no such document")

// Link is one link of graph.links or graph.backlinks (ADR 0204 §4.6). Path is the other end: the
// target of a link, the source of a backlink; "" for an unresolved link.
type Link struct {
	Path     string
	Raw      string // the link as written
	Anchor   string // the heading, or "^" and the block id; "" for none
	Kind     string // LinkWikilink, LinkEmbed or LinkMarkdown
	Resolved bool
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
func kindsIn(q dao.DAO[*store.Link, store.LinkField, int64], kinds []string) dao.DAO[*store.Link, store.LinkField, int64] {
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
		rows, err := kindsIn(s.sc.LinksOut(tx), kinds).With(store.LinkSrc, id).OrderBy(dao.Asc(store.LinkByID)).
			Select(store.LinkOtherPath, store.LinkRaw, store.LinkAnchor, store.LinkKind, store.LinkDst)
		for _, r := range rows {
			out = append(out, Link{Path: r.OtherPath, Raw: r.Raw, Anchor: deref(r.Anchor), Kind: r.Kind, Resolved: r.DstDoc != nil})
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
		rows, err := kindsIn(s.sc.LinksIn(tx), kinds).With(store.LinkDst, id).OrderBy(dao.Asc(store.LinkByOtherPath)).
			Select(store.LinkOtherPath, store.LinkRaw, store.LinkAnchor, store.LinkKind)
		for _, r := range rows {
			out = append(out, Link{Path: r.OtherPath, Raw: r.Raw, Anchor: deref(r.Anchor), Kind: r.Kind, Resolved: true})
		}
		return err
	})
	return out, err
}

// Neighborhood gathers the documents within depth (1 or 2; others are clamped) resolved links of
// the one at path, following links of the given kinds (nil: every kind) both ways, and those links
// among them. The workspace's resolved links of those kinds are walked as a graph.Graph, so a
// document reached only through a link of another kind is not in it.
func (s *Store) Neighborhood(ctx context.Context, path string, depth int, kinds []string) (Neighborhood, error) {
	depth = min(max(depth, 1), 2)
	var nb Neighborhood
	err := s.read(ctx, func(tx *store.Tx) error {
		start, err := s.docID(tx, path)
		if err != nil {
			return err
		}
		rows, err := kindsIn(s.sc.LinksOut(tx), kinds).WithPredicate(dao.IsNotNull(`"link"."dst_doc"`)).
			Select(store.LinkSrc, store.LinkDst, store.LinkKind)
		if err != nil {
			return err
		}
		g := graph.New[int64]()
		g.AddNode(start)
		for _, l := range rows {
			g.Add(graph.Edge[int64]{Src: l.SrcDoc, Dst: *l.DstDoc, Kind: l.Kind})
		}
		sub := g.Neighborhood(start, depth, graph.Both, graph.Filter{})
		var ids []any
		for id := range sub.Nodes() {
			ids = append(ids, id)
		}
		docs, err := s.sc.Documents(tx).With(store.DocID, ids...).Select(store.DocID, store.DocPath)
		if err != nil {
			return err
		}
		paths := map[int64]string{}
		for _, d := range docs {
			paths[d.ID] = d.Path
			nb.Nodes = append(nb.Nodes, d.Path)
		}
		sort.Strings(nb.Nodes)
		for e := range sub.Edges(graph.Filter{}) {
			nb.Edges = append(nb.Edges, Edge{paths[e.Src], paths[e.Dst], e.Kind})
		}
		return nil
	})
	if err != nil {
		return Neighborhood{}, err
	}
	sort.Slice(nb.Edges, func(i, j int) bool {
		a, b := nb.Edges[i], nb.Edges[j]
		if a.Src != b.Src {
			return a.Src < b.Src
		}
		if a.Dst != b.Dst {
			return a.Dst < b.Dst
		}
		return a.Kind < b.Kind
	})
	return nb, nil
}

// Unresolved lists every link of the given kinds (nil: every kind) that reaches no document, by source path, then source order.
func (s *Store) Unresolved(ctx context.Context, kinds []string) ([]Unresolved, error) {
	out := []Unresolved{}
	err := s.read(ctx, func(tx *store.Tx) error {
		rows, err := kindsIn(s.sc.LinksIn(tx), kinds).WithPredicate(dao.IsNull(`"link"."dst_doc"`)).OrderBy(dao.Asc(store.LinkByOtherPath)).
			Select(store.LinkID, store.LinkOtherPath, store.LinkRaw, store.LinkKind, store.LinkName)
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
		return nil
	})
	return out, err
}

func (s *Store) docID(tx *store.Tx, path string) (int64, error) {
	d, err := s.sc.Documents(tx).With(store.DocPath, path).Get(store.DocID)
	if errors.Is(err, dao.ErrNoRows) {
		return 0, ErrNoDocument
	}
	if err != nil {
		return 0, err
	}
	return d.ID, nil
}
