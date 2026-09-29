package index

import (
	"context"
	"errors"
	"sort"

	"github.com/yongjohnlee80/golib/dao"

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

// Links lists the links of the document at path, in source order.
func (s *Store) Links(ctx context.Context, path string) ([]Link, error) {
	out := []Link{}
	err := s.read(ctx, func(tx *store.Tx) error {
		id, err := s.docID(tx, path)
		if err != nil {
			return err
		}
		rows, err := s.sc.LinksOut(tx).With(store.LinkSrc, id).OrderBy(dao.Asc(store.LinkByID)).
			Select(store.LinkOtherPath, store.LinkRaw, store.LinkAnchor, store.LinkKind, store.LinkDst)
		for _, r := range rows {
			out = append(out, Link{Path: r.OtherPath, Raw: r.Raw, Anchor: deref(r.Anchor), Kind: r.Kind, Resolved: r.DstDoc != nil})
		}
		return err
	})
	return out, err
}

// Backlinks lists the links that reach the document at path, by source path, then source order.
func (s *Store) Backlinks(ctx context.Context, path string) ([]Link, error) {
	out := []Link{}
	err := s.read(ctx, func(tx *store.Tx) error {
		id, err := s.docID(tx, path)
		if err != nil {
			return err
		}
		rows, err := s.sc.LinksIn(tx).With(store.LinkDst, id).OrderBy(dao.Asc(store.LinkByOtherPath)).
			Select(store.LinkOtherPath, store.LinkRaw, store.LinkAnchor, store.LinkKind)
		for _, r := range rows {
			out = append(out, Link{Path: r.OtherPath, Raw: r.Raw, Anchor: deref(r.Anchor), Kind: r.Kind, Resolved: true})
		}
		return err
	})
	return out, err
}

// Neighborhood gathers the documents within depth (1 or 2; others are clamped) resolved links of
// the one at path, following links both ways, and the links among them.
func (s *Store) Neighborhood(ctx context.Context, path string, depth int) (Neighborhood, error) {
	depth = min(max(depth, 1), 2)
	var nb Neighborhood
	err := s.read(ctx, func(tx *store.Tx) error {
		start, err := s.docID(tx, path)
		if err != nil {
			return err
		}
		seen := map[int64]bool{start: true}
		frontier := []int64{start}
		for d := 0; d < depth && len(frontier) > 0; d++ {
			var next []int64
			for _, id := range frontier {
				// the documents it links to, and those linking to it
				out, err := s.sc.LinksOut(tx).With(store.LinkSrc, id).WithPredicate(dao.IsNotNull(`"link"."dst_doc"`)).Select(store.LinkDst)
				if err != nil {
					return err
				}
				in, err := s.sc.LinksOut(tx).With(store.LinkDst, id).Select(store.LinkSrc)
				if err != nil {
					return err
				}
				var ns []int64
				for _, l := range out {
					ns = append(ns, *l.DstDoc)
				}
				for _, l := range in {
					ns = append(ns, l.SrcDoc)
				}
				for _, n := range ns {
					if !seen[n] {
						seen[n] = true
						next = append(next, n)
					}
				}
			}
			frontier = next
		}
		ids := make([]any, 0, len(seen))
		for id := range seen {
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
		links, err := s.sc.LinksOut(tx).With(store.LinkSrc, ids...).WithPredicate(dao.IsNotNull(`"link"."dst_doc"`)).
			Select(store.LinkSrc, store.LinkDst, store.LinkKind)
		if err != nil {
			return err
		}
		edges := map[Edge]bool{}
		for _, l := range links {
			if seen[*l.DstDoc] {
				edges[Edge{paths[l.SrcDoc], paths[*l.DstDoc], l.Kind}] = true
			}
		}
		for e := range edges {
			nb.Edges = append(nb.Edges, e)
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

// Unresolved lists every link that reaches no document, by source path, then source order.
func (s *Store) Unresolved(ctx context.Context) ([]Unresolved, error) {
	out := []Unresolved{}
	err := s.read(ctx, func(tx *store.Tx) error {
		rows, err := s.sc.LinksIn(tx).WithPredicate(dao.IsNull(`"link"."dst_doc"`)).OrderBy(dao.Asc(store.LinkByOtherPath)).
			Select(store.LinkOtherPath, store.LinkRaw, store.LinkKind, store.LinkName)
		if err != nil {
			return err
		}
		for _, r := range rows {
			// the reason is not stored: resolving again, in this snapshot, gives the writer's answer
			_, reason, err := s.resolve(tx, r.Kind, r.Name)
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
